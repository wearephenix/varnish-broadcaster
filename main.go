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

var (
	locker    sync.RWMutex
	allCaches []dao.Cache

	addresses = make(map[string]*net.TCPAddr)
	groups    = make(map[string]dao.Group)
	pools     = make(map[string]pool.Pool)

	commandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	port        = commandLine.Int("port", 8088, "Broadcaster port.")
	grCount     = commandLine.Int("goroutines", 2, "Goroutines number. Higher is not implicitly better!")
	reqRetries  = commandLine.Int("retries", 1, "Request retry times if first time fails.")
	caches      = commandLine.String("caches", "/etc/broadcaster/caches.ini", "Path to the default caches configuration file.")
)

func getRequestString(cache dao.Cache) string {
	var reqBuffer = bytes.Buffer{}

	reqBuffer.WriteString(cache.Method)
	reqBuffer.WriteRune(' ')
	reqBuffer.WriteString(cache.Item)
	reqBuffer.WriteString(" HTTP/1.1\r\nHost: ")
	reqBuffer.WriteString(cache.Address)
	reqBuffer.WriteString("\n")

	for k, v := range cache.Headers {
		if strings.HasPrefix(k, "X-") {
			reqBuffer.WriteString(k)
			reqBuffer.WriteString(": ")
			reqBuffer.WriteString(v[0])
			reqBuffer.WriteString("\n")
		}
	}
	reqBuffer.WriteString("\n")

	return reqBuffer.String()
}

func doRequest(cache dao.Cache) ([]byte, error) {

	locker.Lock()
	var connPool = pools[cache.Name]
	locker.Unlock()

	if connPool == nil {
		return nil, errors.New("No pools for " + cache.Name)
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

func worker(jobs <-chan dao.Cache, results chan<- []byte) {
	for j := range jobs {
		var out []byte
		var err error

		for i := 0; i <= *reqRetries; i++ {
			out, err = doRequest(j)
			if err == nil {
				break
			} else {
				err = warmUpConnections(j)
				if err != nil {
					break
				}
			}
		}

		if err != nil {
			results <- []byte(err.Error())
			continue
		}
		results <- out
	}
}

func reqHandler(w http.ResponseWriter, r *http.Request) {

	var (
		buffer          bytes.Buffer
		groupName       string
		broadcastCaches []dao.Cache
	)

	groupName = r.Header.Get("X-Group")

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

	jobs := make(chan dao.Cache, cacheCount)
	results := make(chan []byte, cacheCount)

	for i := 0; i < (*grCount); i++ {
		go worker(jobs, results)
	}

	for _, bc := range broadcastCaches {
		bc.Method = r.Method
		bc.Item = r.URL.Path
		bc.Headers = r.Header
		jobs <- bc
	}

	close(jobs)

	for _, ch := range broadcastCaches {
		buffer.WriteString(ch.Name)
		buffer.WriteString(": ")
		buffer.Write(<-results)
	}

	close(results)

	fmt.Fprint(w, buffer.String())
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

	startBroadcastServer()
}
