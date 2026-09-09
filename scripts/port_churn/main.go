// Command port_churn keeps a large share of the loopback ephemeral port range
// occupied while continuously recycling listeners, which makes races on
// "reserve an ephemeral port, release it, then assume nothing listens there"
// reproduce in seconds instead of once in hundreds of CI runs.
//
// Usage:
//
//	go run ./scripts/port_churn [-hold n] [-duration 60s] [-report 5s]
//
// It exits non-zero if it cannot reach the requested occupancy, so a test run
// is never silently unpressured.
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"time"
)

func main() {
	hold := flag.Int("hold", 40000, "number of loopback listeners to hold open")
	duration := flag.Duration("duration", time.Minute, "how long to keep churning")
	report := flag.Duration("report", 5*time.Second, "status report interval")
	flag.Parse()

	listeners := make([]net.Listener, 0, *hold)
	for len(listeners) < *hold {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fmt.Fprintf(os.Stderr, "only reached %d/%d held listeners: %v\n", len(listeners), *hold, err)
			os.Exit(1)
		}
		listeners = append(listeners, listener)
	}
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()
	fmt.Printf("holding %d loopback ports, churning for %s\n", len(listeners), *duration)

	deadline := time.Now().Add(*duration)
	nextReport := time.Now().Add(*report)
	recycled := 0
	for time.Now().Before(deadline) {
		index := rand.Intn(len(listeners))
		_ = listeners[index].Close()
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			fmt.Fprintf(os.Stderr, "recycle failed after %d recycles: %v\n", recycled, err)
			os.Exit(1)
		}
		listeners[index] = listener
		recycled++
		if time.Now().After(nextReport) {
			fmt.Printf("recycled %d listeners\n", recycled)
			nextReport = time.Now().Add(*report)
		}
	}
	fmt.Printf("done: recycled %d listeners\n", recycled)
}
