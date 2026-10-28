package providers

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"time"
)

// ErrIdleTimeout is reported when no bytes arrive from the stream for the idle duration.
var ErrIdleTimeout = errors.New("sse: idle timeout")

var (
	dataPrefix = []byte("data:")
	doneMarker = []byte("[DONE]")
)

// activityReader signals every successful Read so the idle timer can reset
// even while a single long line is still streaming in.
type activityReader struct {
	r        io.Reader
	activity chan<- struct{}
}

func (a *activityReader) Read(p []byte) (int, error) {
	n, err := a.r.Read(p)
	if n > 0 {
		select {
		case a.activity <- struct{}{}:
		default:
		}
	}
	return n, err
}

type sseLine struct {
	payload []byte
	done    bool
	err     error // io.EOF for clean end
}

// ReadSSE yields each `data:` payload from r until `[DONE]` or EOF. Comment
// lines and blank lines are skipped. It reports ErrIdleTimeout if no bytes
// arrive for idle (idle <= 0 disables the timeout). The error channel yields
// at most one error; both channels close when reading ends.
//
// If the underlying reader blocks forever after a timeout or cancellation, the
// internal read goroutine lingers until the reader is closed by the caller.
func ReadSSE(ctx context.Context, r io.Reader, idle time.Duration) (<-chan []byte, <-chan error) {
	out := make(chan []byte)
	errc := make(chan error, 1)
	activity := make(chan struct{}, 1)
	lines := make(chan sseLine)
	stop := make(chan struct{})

	go readLines(&activityReader{r: r, activity: activity}, lines, stop)

	go func() {
		defer close(out)
		defer close(errc)
		defer close(stop)

		var timeout <-chan time.Time
		var timer *time.Timer
		if idle > 0 {
			timer = time.NewTimer(idle)
			defer timer.Stop()
			timeout = timer.C
		}
		reset := func() {
			if timer == nil {
				return
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(idle)
		}

		for {
			select {
			case <-ctx.Done():
				errc <- ctx.Err()
				return
			case <-timeout:
				errc <- ErrIdleTimeout
				return
			case <-activity:
				reset()
			case l := <-lines:
				if l.err != nil {
					if !errors.Is(l.err, io.EOF) {
						errc <- l.err
					}
					return
				}
				if l.done {
					return
				}
				select {
				case out <- l.payload:
				case <-ctx.Done():
					errc <- ctx.Err()
					return
				}
				reset()
			}
		}
	}()
	return out, errc
}

// readLines parses lines from r and forwards data payloads until stop closes.
func readLines(r io.Reader, lines chan<- sseLine, stop <-chan struct{}) {
	br := bufio.NewReader(r)
	send := func(l sseLine) bool {
		select {
		case lines <- l:
			return true
		case <-stop:
			return false
		}
	}
	for {
		raw, err := br.ReadBytes('\n')
		line := bytes.TrimRight(raw, "\r\n")
		if bytes.HasPrefix(line, dataPrefix) {
			payload := bytes.TrimPrefix(bytes.TrimPrefix(line, dataPrefix), []byte(" "))
			if bytes.Equal(payload, doneMarker) {
				send(sseLine{done: true})
				return
			}
			if !send(sseLine{payload: append([]byte(nil), payload...)}) {
				return
			}
		}
		if err != nil {
			send(sseLine{err: err})
			return
		}
	}
}
