package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/fnv"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mariusmagureanu/broadcaster/dao"
	"github.com/mariusmagureanu/broadcaster/pool"
)

const (
	_NEW_LINE             = "\n"
	_CUSTOM_HEADER_PREFIX = "x-"
	_PROTOCOL_VERSION     = " HTTP/1.1\r\nHost: "
)

var (
	locker    sync.RWMutex
	allCaches []dao.Cache

	addresses = make(map[string]*net.TCPAddr)
	groups    = make(map[string]dao.Group)
	pools     = make(map[string]pool.Pool)

	commandLine   = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	port          = commandLine.Int("port", 8088, "Broadcaster port.")
	grCount       = commandLine.Int("goroutines", 8, "Job handling goroutines pool. Higher is not implicitly better!")
	reqRetries    = commandLine.Int("retries", 1, "Request retry times against a cache - should the first attempt fail.")
	caches        = commandLine.String("caches", "/etc/broadcaster/caches.ini", "Path to the default caches configuration file.")
	logFilePath   = commandLine.String("log-file", "/var/log/broadcaster.log", "Log file path.")
	enforceStatus = commandLine.Bool("enforce", false, "Enforces the status code of a request to be the first encountered non-200 received from a cache. Disabled by default.")
	enableLog     = commandLine.Bool("enable-log", false, "Switches logging on/off. Disabled by default.")

	jobChannel = make(chan *Job, 2<<12)
	logChannel = make(chan []string, 2<<12)
	sigChannel = make(chan os.Signal, 1)

	logBuffer bytes.Buffer
	logFile   *os.File
)

type Job struct {
	Cache  dao.Cache
	Status int
	Result chan []byte
}

func newJob(cache dao.Cache) *Job {
	job := Job{}
	job.Cache = cache
	job.Result = make(chan []byte, 1)
	return &job
}

func hash(s string) string {
	h := fnv.New32a()
	h.Write([]byte(s))
	return fmt.Sprintf("%v", h.Sum32())
}

func sendToLogChannel(args ...string) {
	logChannel <- args
}

// notifySigChannel waits for an Interrupt or Kill signal
// and gracefully handles it.
func notifySigChannel() {
	signal.Notify(sigChannel, os.Interrupt, os.Kill)

	go func() {
		<-sigChannel
		if *enableLog {
			if logFile != nil {
				logFile.Close()
			}
		}

		fmt.Println("Broadcaster exited succesfully.")
		os.Exit(0)
	}()
}

// startLog initializes and starts a goroutine that's going
// to listen the logChannel and write any entries that come along.
func startLog() error {
	if *logFilePath != "" {
		var logFileErr error
		logFile, logFileErr = os.OpenFile(*logFilePath, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)

		if logFileErr != nil {
			return logFileErr
		}

		go func() {
			for logEntry := range logChannel {
				logBuffer.Reset()
				logBuffer.WriteString(time.Now().Format(time.RFC3339))
				logBuffer.WriteString(" ")

				for _, logString := range logEntry {
					logBuffer.WriteString(logString)
				}

				logFile.WriteString(logBuffer.String())
			}
		}()
	}
	return nil
}

// getRequestString generates a http compliant request
// for a given cache.
func getRequestString(cache dao.Cache) string {
	var reqBuffer = bytes.Buffer{}

	reqBuffer.WriteString(cache.Method)
	reqBuffer.WriteRune(' ')
	reqBuffer.WriteString(cache.Item)
	reqBuffer.WriteString(_PROTOCOL_VERSION)
	reqBuffer.WriteString(cache.Address)
	reqBuffer.WriteString(_NEW_LINE)

	for k, v := range cache.Headers {
		if strings.HasPrefix(strings.ToLower(k), _CUSTOM_HEADER_PREFIX) {
			reqBuffer.WriteString(k)
			reqBuffer.WriteString(": ")
			reqBuffer.WriteString(v[0])
			reqBuffer.WriteString(_NEW_LINE)
		}
	}
	reqBuffer.WriteString(_NEW_LINE)

	return reqBuffer.String()
}

// doRequest retrieves an available connection from the pool
// and writes into it. If succesfull, the connection is sent back to
// the pool, otherwise - for any error - the pool is closed.
func doRequest(cache dao.Cache) ([]byte, error) {

	locker.Lock()
	var connPool = pools[cache.Name]
	locker.Unlock()

	if connPool == nil {
		return nil, errors.New("No connection pool available for " + cache.Name)
	}

	tcpConnection, err := connPool.Get()

	if err != nil {

		connPool.Close()
		return nil, err
	}

	defer tcpConnection.Close()

	reqString := getRequestString(cache)
	_, err = tcpConnection.Write([]byte(reqString))

	if err != nil {
		connPool.Close()
		return nil, err
	}

	var reader = bufio.NewReader(tcpConnection)
	out, err := reader.ReadBytes('\n')

	if err != nil {
		connPool.Close()
		return nil, err
	}
	return out, nil
}

// jobWorker listens on the jobs channel and handles
// any incoming job.
func jobWorker(jobs <-chan *Job) {
	for job := range jobs {
		var out []byte
		var err error

		for i := 0; i <= *reqRetries; i++ {
			out, err = doRequest(job.Cache)
			if err == nil {
				break
			} else {
				err = warmUpConnections(job.Cache)
				if err != nil {
					break
				}
			}
		}

		if err != nil {
			job.Result <- []byte(err.Error())
			continue
		}
		job.Result <- out
	}
}

// reqHandler handles any incoming http request. Its main purpose
// is to distribute the request further to all required caches.
func reqHandler(w http.ResponseWriter, r *http.Request) {

	var (
		err             error
		groupName       string
		reqId           string
		broadcastCaches []dao.Cache
		statusCode      = http.StatusOK
		respBody        = make(map[string]int)
	)

	for k, v := range r.Header {
		if strings.ToLower(k) == "x-group" {
			groupName = v[0]
			break
		}
	}

	switch groupName {
	case "":
		http.Error(w, "Missing group name.", http.StatusBadRequest)
		return
	case "all":
		broadcastCaches = allCaches
		break
	default:
		if _, found := groups[groupName]; !found {
			http.Error(w, "Group not found.", http.StatusNotFound)
			return
		}
		broadcastCaches = groups[groupName].Caches
	}

	var cacheCount = len(broadcastCaches)

	if cacheCount == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var jobs = make([]*Job, cacheCount)

	for idx, bc := range broadcastCaches {
		bc.Method = r.Method
		bc.Item = r.URL.Path
		bc.Headers = r.Header

		job := newJob(bc)
		jobs[idx] = job
		jobChannel <- job
	}

	if *enableLog {
		reqId = hash(hash(time.Now().String()))
	}

	for _, job := range jobs {
		result := <-job.Result
		resAsString := string(result)

		job.Status, err = strconv.Atoi(strings.Fields(resAsString)[1])

		if err != nil {
			job.Status = http.StatusInternalServerError
		}

		if *enforceStatus && statusCode == http.StatusOK {
			statusCode = job.Status
		}

		respBody[job.Cache.Name] = job.Status

		if *enableLog {
			sendToLogChannel(reqId, " ", r.Method, " ", job.Cache.Address, r.URL.Path, " ", resAsString)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	out, _ := json.MarshalIndent(respBody, "", "  ")
	w.Write(out)
}

func startBroadcastServer() {
	http.HandleFunc("/", reqHandler)
	fmt.Fprintf(os.Stdout, "Broadcaster serving on %s...\n", strconv.Itoa(*port))
	fmt.Println(http.ListenAndServe(":"+strconv.Itoa(*port), nil))
}

// setUpCaches reads the configured caches from the .ini file
// and populates a map having group name as key and slice of caches
// as values.
func setUpCaches() error {
	groupList, err := dao.LoadCachesFromIni(*caches)

	for _, g := range groupList {
		groups[g.Name] = g
	}
	return err
}

// resolveCacheTcpAddresses iterates the configured caches and tries
// to resolve their addresses, if succesfull - a map with cache names and
// their resolved addresses is populated.
func resolveCacheTcpAddresses() error {
	var err error
	for _, group := range groups {
		for _, cache := range group.Caches {

			cacheTcpAddress, err := net.ResolveTCPAddr("tcp4", cache.Address)

			if err != nil {
				return err
			}

			addresses[cache.Name] = cacheTcpAddress
			allCaches = append(allCaches, cache)
		}
	}
	return err
}

// warmpUpConnections creates a pool of 10 connections
// for the specified cache.
func warmUpConnections(cache dao.Cache) error {

	locker.Lock()
	defer locker.Unlock()

	cacheTcpAddress := addresses[cache.Name]

	factory := func() (net.Conn, error) {
		tcpConn, err := net.DialTCP("tcp", nil, cacheTcpAddress)
		if err != nil {
			return nil, err
		}
		tcpConn.SetKeepAlive(true)
		tcpConn.SetKeepAlivePeriod(1 * time.Minute)
		return tcpConn, err
	}

	p, err := pool.NewChannelPool(10, 40, factory)

	if err != nil {
		return err
	}

	pools[cache.Name] = p

	return nil
}

func main() {
	var err error

	commandLine.Usage = func() {
		fmt.Fprint(os.Stdout, "Usage of the broadcaster:\n")
		commandLine.PrintDefaults()
	}

	if err := commandLine.Parse(os.Args[1:]); err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	runtime.GOMAXPROCS(runtime.NumCPU() - 1)

	fmt.Println("Loading caches configuration.")
	err = setUpCaches()
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	err = resolveCacheTcpAddresses()
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	if *enableLog {
		err = startLog()
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
		fmt.Fprintf(os.Stdout, "Logging to %s\n", *logFilePath)
		defer logFile.Close()
	}

	notifySigChannel()

	fmt.Println("Warming up connections.")

	for _, cache := range allCaches {
		err = warmUpConnections(cache)
		if err != nil {
			fmt.Fprintf(os.Stderr, "* Cache [%s] encountered an error when warming up connections.\n    - %s\n", cache.Name, err.Error())
		}
	}

	for i := 0; i < (*grCount); i++ {
		go jobWorker(jobChannel)
	}

	startBroadcastServer()
}
