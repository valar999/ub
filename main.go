package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"sync"
	"syscall"
	"time"
)

type Stat struct {
	connect time.Duration
	err     error
}

const ConnectTimeout = time.Second * 3

var addr string
var numConns int
var statsChan chan Stat = make(chan Stat)

func worker(ctx context.Context) {
	for {
		stat := Stat{}
		dialer := new(net.Dialer)
		dialer.Timeout = ConnectTimeout
		start := time.Now()
		conn, err := dialer.DialContext(ctx, "tcp", addr)
		if err != nil {
			stat.err = err
		} else {
			stat.connect = time.Since(start)
			go conn.Close()
		}
		select {
		case statsChan <- stat:
		case <-ctx.Done():
			return
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
	reqPerSec := float64(n) / duration.Seconds()
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
	}
	fmt.Printf("Req/sec: %v\n", reqPerSec)
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
	var durationInt int
	flag.IntVar(&numConns, "c", 1, "connections to keep open")
	flag.IntVar(&durationInt, "d", 10, "duration of test")
	_ = flag.String("s", "", "lua script file")
	flag.Parse()
	addr = flag.Arg(0)
	duration := time.Second * time.Duration(durationInt)

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	conn.Close()

	stats := make([]Stat, 0, numConns)
	errs := make([]Stat, 0)
	go func() {
		for {
			select {
			case stat := <-statsChan:
				if stat.err == nil {
					stats = append(stats, stat)
				} else {
					errs = append(errs, stat)
				}
			}
		}
	}()
	var wg sync.WaitGroup
	wg.Add(numConns)
	ctx, _ := context.WithTimeout(context.Background(), duration)
	for i := 0; i < numConns; i++ {
		go func() {
			worker(ctx)
			wg.Done()
		}()
	}
	wg.Wait()
	showStat(stats, errs, duration)
}
