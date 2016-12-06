package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
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

	commandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	port        = commandLine.Int("port", 8088, "Broadcaster port.")
	grCount     = commandLine.Int("goroutines", 8, "Goroutines number. Higher is not implicitly better!")
	reqRetries  = commandLine.Int("retries", 1, "Request retry times if first time fails.")
	caches      = commandLine.String("caches", "/etc/broadcaster/caches.ini", "Path to the default caches configuration file.")

	jobChannel = make(chan *Job, 2<<12)
)

type Job struct {
	Cache  dao.Cache
	Result chan []byte
}

func NewJob(cache dao.Cache) *Job {
	job := Job{}
	job.Cache = cache
	job.Result = make(chan []byte, 1)
	return &job
}

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

	purgeReq := getRequestString(cache)
	_, err = tcpConnection.Write([]byte(purgeReq))

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

func reqHandler(w http.ResponseWriter, r *http.Request) {

	var (
		buffer          bytes.Buffer
		groupName       string
		broadcastCaches []dao.Cache
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

		job := NewJob(bc)
		jobs[idx] = job
		jobChannel <- job
	}

	for _, job := range jobs {
		buffer.WriteString(job.Cache.Name)
		buffer.WriteString(": ")
		buffer.Write(<-job.Result)
	}

	w.Write(buffer.Bytes())
}

func startBroadcastServer() {
	http.HandleFunc("/", reqHandler)
	fmt.Fprintf(os.Stdout, "Broadcaster serving on %s...\n", strconv.Itoa(*port))
	fmt.Println(http.ListenAndServe(":"+strconv.Itoa(*port), nil))
}

func setUpCaches() error {
	groupList, err := dao.LoadCachesFromIni(*caches)

	for _, g := range groupList {
		groups[g.Name] = g
	}
	return err
}

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
