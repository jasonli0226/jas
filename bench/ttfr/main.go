// Command ttfr measures time from process start to the first HTTP request
// `jas run` sends, against a local listener that only records arrival time.
//
//	go run ./bench/ttfr /path/to/jas
package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"time"
)

const runs = 20

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: ttfr /path/to/jas")
		os.Exit(2)
	}
	bin := os.Args[1]
	var durs []time.Duration
	for i := 0; i < runs; i++ {
		d, err := once(bin)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ttfr:", err)
			os.Exit(1)
		}
		durs = append(durs, d)
	}
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	fmt.Printf("time-to-first-request: median %v  min %v  max %v  (%d runs)\n",
		durs[len(durs)/2].Round(time.Microsecond), durs[0].Round(time.Microsecond),
		durs[len(durs)-1].Round(time.Microsecond), runs)
}

func once(bin string) (time.Duration, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	arrived := make(chan time.Time, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case arrived <- time.Now():
		default:
		}
		http.Error(w, "bench listener", http.StatusBadRequest)
	})}
	go srv.Serve(ln)
	defer srv.Close()

	home, err := os.MkdirTemp("", "jas-ttfr-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(home)
	work, err := os.MkdirTemp("", "jas-ttfr-work-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(work)

	cmd := exec.Command(bin, "run", "noop")
	cmd.Dir = work
	cmd.Env = append(os.Environ(),
		"JAS_HOME="+home,
		"JAS_BASE_URL=http://"+ln.Addr().String(),
		"JAS_MODEL=bench/model",
		"JAS_API_KEY=bench-not-a-real-key",
	)
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	select {
	case t := <-arrived:
		return t.Sub(start), nil
	case <-time.After(10 * time.Second):
		return 0, fmt.Errorf("no request within 10s")
	}
}
