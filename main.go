package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"sync"
	"syscall"
	"time"
)

type Stat struct {
	connect time.Duration
	reply   time.Duration
	cmd     time.Duration
	err     error
}

const ConnectTimeout = time.Second * 3

var addr string
var numConns int
var statsChan chan Stat = make(chan Stat)
var startWG sync.WaitGroup

func worker(ctx context.Context) {
	startWG.Done()
	for {
		stat := Stat{}
		dialer := new(net.Dialer)
		dialer.Timeout = ConnectTimeout
		start := time.Now()
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err == nil {
			stat.connect = time.Since(start)
			/*
				go func() {
					// SO_LINGER=0 reset conn
					// no TIME_WAIT conn left
					conn.(*net.TCPConn).SetLinger(0)
					conn.Close()
				}()
			*/
		} else {
			select {
			case <-ctx.Done():
				return
			default:
			}
			stat.err = err
		}

		var n int
		buf := make([]byte, 1024)
		start = time.Now()
		_, err = conn.Write([]byte(`{"id": 1, "method": "mining.subscribe", "params": ["MinerName/1.0.0", "EthereumStratum/1.0.0"]}`))
		if err != nil {
			log.Println("err", err)
			select {
			case <-ctx.Done():
				return
			default:
			}
			stat.err = err
		}
		n, err = conn.Read(buf)
		log.Println("recv1", err, n, string(buf[:n]))

		n, err = conn.Read(buf)
		log.Println("recv2", err, n, string(buf[:n]))

		n, err = conn.Read(buf)
		log.Println("recv3", err, n, string(buf[:n]))

		go func() {
			// SO_LINGER=0 reset conn
			// no TIME_WAIT conn left
			conn.(*net.TCPConn).SetLinger(0)
			conn.Close()
		}()

		select {
		case <-ctx.Done():
			return
		case statsChan <- stat:
		}
	}
}

func showStat(stats []Stat, errs []Stat, duration time.Duration) {
	var n time.Duration
	var sumConnect, minConnect, maxConnect time.Duration
	var avgConnect, stdConnect time.Duration
	var stdConnectFloat float64
	for _, stat := range stats {
		n++
		sumConnect += stat.connect
		if stat.connect < minConnect || minConnect == 0 {
			minConnect = stat.connect
		}
		if stat.connect > maxConnect {
			maxConnect = stat.connect
		}
	}
	if n > 1 {
		avgConnect = sumConnect / n
		for _, stat := range stats {
			stdConnectFloat += math.Pow(
				float64(stat.connect-avgConnect), 2)
		}
		stdConnect = time.Duration(math.Sqrt(
			(stdConnectFloat / (float64(n) - 1))))
	}
	reqPerSec := float64(len(stats)) / duration.Seconds()
	fmt.Printf("Running %v test of %v\n", duration, addr)
	fmt.Printf("%v concurrent connections\n", numConns)
	fmt.Printf("\n")
	fmt.Printf("\tAvg\tMin\tMax\tStdev\n")
	fmt.Printf("connect\t%v\t%v\t%v\t%v\n",
		avgConnect.Truncate(time.Millisecond),
		minConnect.Truncate(time.Millisecond),
		maxConnect.Truncate(time.Millisecond),
		stdConnect.Truncate(time.Millisecond))
	fmt.Printf("\n")
	if len(errs) > 0 {
		fmt.Printf("Errors: %v (%.1f%%)\n", len(errs),
			float64(len(errs))/float64(len(stats)+len(errs))*100)
		errTypes := make(map[string]int)
		for _, stat := range errs {
			var s string
			switch t := stat.err.(type) {
			//case *net.OpError:
			//	s = t.Err
			default:
				s = t.Error()
			}
			errTypes[s]++
		}
		for err, n := range errTypes {
			s := err
			switch err {
			case "dial":
				s = "connect"
			}
			fmt.Printf("  %v\t%v\n", n, s)
		}
	}
	fmt.Printf("Req/sec: %.1f\n", reqPerSec)
}

func setLimits(nofile uint64) {
	var rLimit syscall.Rlimit
	rLimit.Max = nofile
	rLimit.Cur = nofile
	err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &rLimit)
	if err != nil {
		panic("Setrlimit")
	}
}

func main() {
	setLimits(100000)
	log.SetFlags(log.Lmicroseconds)
	var durationInt int
	flag.IntVar(&numConns, "c", 1, "connections to keep open")
	flag.IntVar(&durationInt, "d", 10, "duration of test")
	_ = flag.String("s", "", "lua script file")
	flag.Parse()
	addr = flag.Arg(0)
	duration := time.Second * time.Duration(durationInt)

	conn, err := net.DialTimeout("tcp", addr, ConnectTimeout)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	conn.Close()

	stats := make([]Stat, 0, numConns*int(duration.Seconds()))
	errs := make([]Stat, 0)
	startChan := make(chan bool)
	stopChan := make(chan bool)
	go func() {
		var started bool
		for {
			select {
			case stat := <-statsChan:
				if started {
					if stat.err == nil {
						stats = append(stats, stat)
					} else {
						errs = append(errs, stat)
					}
				}
			case <-startChan:
				started = true
			case <-stopChan:
				return
			}
		}
	}()
	var wg sync.WaitGroup
	wg.Add(numConns)
	startWG.Add(numConns)
	ctx, cancel := context.WithCancel(context.Background())
	for i := 0; i < numConns; i++ {
		go func() {
			worker(ctx)
			wg.Done()
		}()
	}
	startWG.Wait()
	startChan <- true
	time.Sleep(duration)
	stopChan <- true
	time.Sleep(time.Millisecond * 100)
	cancel()
	wg.Wait()
	showStat(stats, errs, duration)
}
