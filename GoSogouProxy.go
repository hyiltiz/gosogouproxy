// encoding: utf-8
/*
 * Copyright (C) 2014--2015 Liú Hǎiyáng
 *
 * Permission is hereby granted, free of charge, to any person obtaining a
 * copy of this software and associated documentation files (the "Software"),
 * to deal in the Software without restriction, including without limitation
 * the rights to use, copy, modify, merge, publish, distribute, sublicense,
 * and/or sell copies of the Software, and to permit persons to whom the
 * Software is furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in
 * all copies or substantial portions of the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
 * IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
 * FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
 * AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
 * LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING
 * FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER
 * DEALINGS IN THE SOFTWARE.
 */

// Sogou Proxy
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"net/http"
	"os"
	"sync"
	"time"
)

// Build with option: -ldflags "-X main.Revision ?"
var Revision string = "???"

const (
	defalutTimeOut  = 200 * time.Millisecond // Connection timeout
	refreshDuration = 30 * time.Minute       // Regular refresh
	waitDuration    = 10 * time.Second       // Force refresh
)

func main() {
	var quiet bool
	flag.BoolVar(&quiet, "q", false, "Quiet mode. (Disable console output)")
	var logEnabled bool
	flag.BoolVar(&logEnabled, "l", false, "Enable log file.")
	var serverPort uint
	flag.UintVar(&serverPort, "p", 8008, "Set server port.")
	var proxyTypeStr string
	flag.StringVar(&proxyTypeStr, "t", "edu", "Select type of proxy: edu, dxt, cnc, ctc, tc9")

	flag.Parse()

	setLog(quiet, logEnabled)

	if _, ok := proxyTypeMap[proxyTypeStr]; !ok {
		fmt.Fprintf(os.Stderr, "Unknown proxy type '%s'.\n", proxyTypeStr)
		os.Exit(0)
	}

	log.Printf("GoSogouProxy (rev. %s), Copyright (C) 2014--2015 Liu Haiyang\n", Revision)
	log.Println("This software is released under The MIT License.")

	handler := &SogouProxyHandler{
		ProxyType:         proxyTypeMap[proxyTypeStr],
		timeOut:           defalutTimeOut,
		getRequestChan:    make(chan chan int),
		disableReqestChan: make(chan int),
	}
	go hostlistDaemon(handler)
	serverAddr := fmt.Sprintf("127.0.0.1:%d", serverPort)
	log.Printf("Start serving on %s\n", serverAddr)
	err := http.ListenAndServe(serverAddr, handler)
	if err != nil {
		log.Fatalf("Cannot serve on %s: %s", serverAddr, err)
	}
}

func setLog(quiet, logEnabled bool) {
	var (
		console   io.Writer = os.Stderr
		logwriter io.Writer = ioutil.Discard
	)
	if quiet {
		console = ioutil.Discard
	}
	if logEnabled {
		logfile, err := os.Create("gosogouproxy.log")
		if err != nil {
			log.Println("Cannot create log file: %s", err)
		} else {
			logwriter = logfile
		}
	}
	log.SetOutput(io.MultiWriter(logwriter, console))
}

type ProxyType struct {
	hostTemplate string
	hostMax      int
}

var proxyTypeMap = map[string]ProxyType{
	"edu": {hostTemplate: "h%d.edu.bj.ie.sogou.com:80", hostMax: 16},
	"dxt": {hostTemplate: "h%d.dxt.bj.ie.sogou.com:80", hostMax: 16},
	"cnc": {hostTemplate: "h%d.cnc.bj.ie.sogou.com:80", hostMax: 4},
	"ctc": {hostTemplate: "h%d.ctc.bj.ie.sogou.com:80", hostMax: 4},
	"tc9": {hostTemplate: "h%d.tc9.bj.ie.sogou.com:80", hostMax: 16},
}

type SogouProxyHandler struct {
	ProxyType
	timeOut           time.Duration
	getRequestChan    chan chan int
	disableReqestChan chan int
}

func (handler *SogouProxyHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	proxyConn := mustDialSogou(handler)

	timestamp := fmt.Sprintf("%08x", time.Now().Unix())
	request.Header.Add("X-Sogou-Timestamp", timestamp)
	tag := fmt.Sprintf("%08x", sogouTagHash(timestamp+request.Host+"SogouExplorerProxy"))
	request.Header.Add("X-Sogou-Tag", tag)
	request.Header.Add("X-Sogou-Auth", "58C41A7C258CAB58167E110BB5DEF7AF/4.1.3.8107/md5")

	request.WriteProxy(proxyConn)

	hj, ok := writer.(http.Hijacker)
	if !ok {
		log.Println("ERROR: ", "webserver doesn't support hijacking", http.StatusInternalServerError)
		http.Error(writer, "webserver doesn't support hijacking", http.StatusInternalServerError)
		return
	}
	clientConn, _, err := hj.Hijack()
	if err != nil {
		log.Println("ERROR: ", err, http.StatusInternalServerError)
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}

	proxyBufReader := bufio.NewReader(proxyConn)
	response, err := http.ReadResponse(proxyBufReader, request)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	if request.Method == "CONNECT" {
		response.Body.Close()
	} else {
		defer response.Body.Close()
	}

	log.Printf("%s %s %s\n<- %s\n", request.RemoteAddr, request.Method, request.RequestURI, response.Status)
	response.Write(clientConn)

	if request.Method == "CONNECT" && response.StatusCode == http.StatusOK {
		go copyAndClose(proxyConn, clientConn)
		go copyAndClose(clientConn, proxyBufReader)
	} else {
		clientConn.Close()
		proxyConn.Close()
	}
}

func mustDialSogou(handler *SogouProxyHandler) net.Conn {
	for {
		req := make(chan int)
		handler.getRequestChan <- req
		proxyNum := <-req
		proxyHost := fmt.Sprintf(handler.hostTemplate, proxyNum)
		proxyConn, err := net.DialTimeout("tcp", proxyHost, handler.timeOut)
		if err == nil {
			log.Printf("Dial to h%d: ok\n", proxyNum)
			return proxyConn
		} else {
			log.Printf("Dial to h%d: failed. %s\n", proxyNum, err)
			handler.disableReqestChan <- proxyNum
		}
	}
}

func hostlistDaemon(handler *SogouProxyHandler) {
	isHostValid := make([]bool, handler.hostMax)
	hostlist := refreshHostlist(handler, isHostValid)
	freshChan := make(chan []int)
	for {
		select {
		case getreq := <-handler.getRequestChan:
			if len(hostlist) > 0 {
				i := rand.Intn(len(hostlist))
				getreq <- hostlist[i]
			} else {
				// Stop and refresh
				hostlist = refreshHostlist(handler, isHostValid)
			}
		case delreq := <-handler.disableReqestChan:
			if len(hostlist) > 0 {
				isHostValid[delreq] = false
				hostlist = getList(isHostValid)
			} else {
				// Stop and refresh
				hostlist = refreshHostlist(handler, isHostValid)
			}
		case <-time.After(refreshDuration):
			// Regular updating, we don't stop the world.
			go func() { freshChan <- refreshHostlist(handler, isHostValid) }()
		case newlist := <-freshChan:
			// Regular update
			hostlist = newlist
		}
	}
}

func refreshHostlist(handler *SogouProxyHandler, isHostValid []bool) []int {
	type signal struct{}
	for {
		log.Println("Updating available proxy host list...")
		log.Printf("%s -- %s\n", fmt.Sprintf(handler.hostTemplate, 0), fmt.Sprintf(handler.hostTemplate, handler.hostMax-1))
		var waiter sync.WaitGroup
		waiter.Add(handler.hostMax)
		for i := 0; i < handler.hostMax; i++ {
			go func(ihost int) {
				proxyHost := fmt.Sprintf(handler.hostTemplate, ihost)
				conn, err := net.DialTimeout("tcp", proxyHost, handler.timeOut)
				if err != nil {
					log.Printf("Host %d unavailable: %s\n", ihost, err)
					isHostValid[ihost] = false
				} else {
					log.Printf("Host %d OK (%s).\n", ihost, conn.RemoteAddr())
					conn.Close()
					isHostValid[ihost] = true
				}
				waiter.Done()
			}(i)
		}
		waiter.Wait()
		hostlist := getList(isHostValid)
		// Not even one proxy host avaiable
		if len(hostlist) > 0 {
			log.Println("Available proxy host list is updated.")
			return hostlist
		} else {
			log.Printf("All hosts are unavailable. Try again after %s.", waitDuration)
			time.Sleep(waitDuration)
		}
	}
}

func getList(isValid []bool) []int {
	list := make([]int, 0, len(isValid))
	for i := 0; i < len(isValid); i++ {
		if isValid[i] {
			list = append(list, i)
		}
	}
	return list
}

func copyAndClose(w io.WriteCloser, r io.Reader) {
	io.Copy(w, r)
	if err := w.Close(); err != nil {
		log.Println("Error closing", err)
	}
}

// SougouExplorer 4.1.3.8107
// SENetLayer.dll .text:35664A95
func sogouTagHash(s string) uint32 {
	n := len(s)
	if n == 0 {
		return 0
	}
	hash := uint32(n)
	i := 0
	for ndword := n / 4; ndword > 0; ndword-- {
		loword := uint32(s[i+1])<<8 | uint32(s[i])
		hiword := uint32(s[i+3])<<8 | uint32(s[i+2])
		hash += loword
		hash ^= (hiword ^ hash<<5) << 11
		hash += hash >> 11
		i += 4
	}
	switch n % 4 {
	case 1:
		hash += uint32(s[i])
		hash ^= hash << 10
		hash += hash >> 1
	case 2:
		hash += uint32(s[i+1])<<8 | uint32(s[i])
		hash ^= hash << 11
		hash += hash >> 17
	case 3:
		hash += uint32(s[i+1])<<8 | uint32(s[i])
		hash ^= (hash ^ uint32(s[i+2])<<2) << 16
		hash += hash >> 11
	}
	hash ^= hash << 3
	hash += hash >> 5
	hash ^= hash << 4
	hash += hash >> 17
	hash ^= hash << 25
	hash += hash >> 6
	return hash
}
