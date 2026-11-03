package core

import (
	"errors"
	"testing"
	"time"
)

// failingReader returns one line, then the same error on every read.
type failingReader struct{ sent bool }

func (r *failingReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, "{}\n"), nil
	}
	return 0, errors.New("disk on fire")
}

func TestLogScanner_StopsOnReadError(t *testing.T) {
	sc := newLogScanner(&failingReader{})
	done := make(chan int, 1)
	go func() {
		n := 0
		for {
			if _, _, ok := sc.next(); !ok {
				break
			}
			if n++; n > 10 {
				break
			}
		}
		done <- n
	}()
	select {
	case n := <-done:
		if n != 1 {
			t.Fatalf("read %d lines past a persistent error; want 1", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("next() looped on a read error")
	}
	if sc.Err() == nil {
		t.Fatal("read error not reported by Err")
	}
}
