package main

import (
	"bytes"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"

	"bufio"
	"github.com/mariusmagureanu/broadcaster/broadcaster"
	"github.com/mariusmagureanu/broadcaster/dao"
	"github.com/mariusmagureanu/broadcaster/pool"
	"time"
)

const (
	PURGE_METHOD = "PURGE"
	BAN_METHOD   = "BAN"
)

var (
	allCaches []dao.Cache

	runners   = make(map[string]broadcasters.Broadcaster)
	addresses = make(map[string]*net.TCPAddr)
	groups    = make(map[string]dao.Group)
	pools     = make(map[string]pool.Pool)

	commandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	port        = commandLine.Int("port", 8088, "Broadcaster port.")
	grCount     = commandLine.Int("goroutines", 2, "Goroutines number. Higher is not implicitly better!")
)

func doRequest(cache dao.Cache) ([]byte, error) {

	var (
		reqBuffer = bytes.Buffer{}
		coonPool  = pools[cache.Address]
	)

	tcpConnection, err := coonPool.Get()
	if err != nil {
		coonPool.Close()
		return nil, err
	}
	defer tcpConnection.Close()

	reqBuffer.WriteString(cache.Method)
	reqBuffer.WriteRune(' ')
	reqBuffer.WriteString(cache.Item)
	reqBuffer.WriteString(" HTTP/1.1\r\nHost: ")
	reqBuffer.WriteString(cache.Address)
	reqBuffer.WriteString("\n\n")

	purgeReq := reqBuffer.String()

	_, err = tcpConnection.Write([]byte(purgeReq))
	if err != nil {
		coonPool.Close()
		return nil, err
	}

	var reader = bufio.NewReader(tcpConnection)
	return reader.Peek(12)
}

func worker(jobs <-chan dao.Cache, results chan<- []byte) {
	for j := range jobs {
		out, err := doRequest(j)
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

	jobs := make(chan dao.Cache, cacheCount+1)
	results := make(chan []byte, cacheCount+1)

	for i := 0; i < (*grCount); i++ {
		go worker(jobs, results)
	}

	for _, bc := range broadcastCaches {
		bc.Method = r.Method
		bc.Item = r.URL.Path
		jobs <- bc
	}

	for _, ch := range broadcastCaches {
		buffer.WriteString(ch.Name)
		buffer.WriteString(": ")
		buffer.Write(<-results)
		buffer.WriteRune('\n')
	}

	close(jobs)
	close(results)

	fmt.Fprint(w, buffer.String())
}

func startBroadcastServer() {

	runners[BAN_METHOD] = broadcasters.Banner{}
	runners[PURGE_METHOD] = broadcasters.Purger{}

	http.HandleFunc("/", reqHandler)

	fmt.Fprintf(os.Stdout, "Starting to serve on %s...", strconv.Itoa(*port))
	fmt.Println(http.ListenAndServe(":"+strconv.Itoa(*port), nil))
}

func setUpCaches() error {
	groupList, err := dao.LoadCaches("./caches.json")

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

			addresses[cache.Address] = cacheTcpAddress
			allCaches = append(allCaches, cache)
		}
	}
	return err
}

func warmUpConnections() error {

	for _, cache := range allCaches {
		cacheTcpAddress := addresses[cache.Address]

		factory := func() (net.Conn, error) {
			tcpConn, err := net.DialTCP("tcp", nil, cacheTcpAddress)
			if err != nil {
				return nil, err
			}
			tcpConn.SetKeepAlive(true)
			tcpConn.SetKeepAlivePeriod(1 * time.Minute)
			return tcpConn, err
		}

		p, err := pool.NewChannelPool(50, 200, factory)

		if err != nil {
			return err
		}

		pools[cache.Address] = p

	}
	return nil
}

func main() {
	var err error

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

	err = warmUpConnections()
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	commandLine.Usage = func() {
		fmt.Fprint(os.Stdout, "Usage of the broadcaster:\n")
		commandLine.PrintDefaults()
	}

	if err := commandLine.Parse(os.Args[1:]); err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	startBroadcastServer()
}
