package farm

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// lrOut is a concurrency-safe writer that lets tests wait for output.
type lrOut struct {
	mu  sync.Mutex
	buf strings.Builder
	sig chan struct{}
}

func newLROut() *lrOut { return &lrOut{sig: make(chan struct{}, 64)} }

func (o *lrOut) Write(p []byte) (int, error) {
	o.mu.Lock()
	o.buf.Write(p)
	o.mu.Unlock()
	select {
	case o.sig <- struct{}{}:
	default:
	}
	return len(p), nil
}

func (o *lrOut) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

// waitFor blocks until the output contains s.
func (o *lrOut) waitFor(t *testing.T, s string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for !strings.Contains(o.String(), s) {
		select {
		case <-o.sig:
		case <-deadline:
			t.Fatalf("timed out waiting for %q; output %q", s, o.String())
		}
	}
}

type askResult struct {
	line string
	err  error
}

func askAsync(l *LineReader, ctx context.Context, prompt string) <-chan askResult {
	ch := make(chan askResult, 1)
	go func() {
		line, err := l.Ask(ctx, prompt)
		ch <- askResult{line, err}
	}()
	return ch
}

func recvResult(t *testing.T, ch <-chan askResult) askResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Ask")
		return askResult{}
	}
}

func TestLineReader_Answer(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	out := newLROut()
	l := NewLineReader(pr, out)
	res := askAsync(l, context.Background(), "ok? ")
	out.waitFor(t, "ok? ")
	if _, err := pw.Write([]byte("y\n")); err != nil {
		t.Fatal(err)
	}
	r := recvResult(t, res)
	if r.err != nil || r.line != "y" {
		t.Fatalf("got %q, %v; want y, nil", r.line, r.err)
	}
}

func TestLineReader_CancelThenNext(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	out := newLROut()
	l := NewLineReader(pr, out)
	ctxA, cancelA := context.WithCancel(context.Background())
	a := askAsync(l, ctxA, "A? ")
	out.waitFor(t, "A? ")
	cancelA()
	if r := recvResult(t, a); r.err != context.Canceled {
		t.Fatalf("A err = %v, want context.Canceled", r.err)
	}
	b := askAsync(l, context.Background(), "B? ")
	out.waitFor(t, "B? ")
	if _, err := pw.Write([]byte("n\n")); err != nil {
		t.Fatal(err)
	}
	if r := recvResult(t, b); r.err != nil || r.line != "n" {
		t.Fatalf("B got %q, %v; want n, nil", r.line, r.err)
	}
}

func TestLineReader_LateLineNotPassedOn(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	out := newLROut()
	l := NewLineReader(pr, out)
	ctxA, cancelA := context.WithCancel(context.Background())
	a := askAsync(l, ctxA, "A? ")
	out.waitFor(t, "A? ")
	cancelA()
	recvResult(t, a)

	if _, err := pw.Write([]byte("y\n")); err != nil {
		t.Fatal(err)
	}
	out.waitFor(t, "(ignored: no question is waiting)")

	b := askAsync(l, context.Background(), "B? ")
	out.waitFor(t, "B? ")
	if _, err := pw.Write([]byte("n\n")); err != nil {
		t.Fatal(err)
	}
	if r := recvResult(t, b); r.err != nil || r.line != "n" {
		t.Fatalf("B got %q, %v; want n, nil", r.line, r.err)
	}
}

func TestLineReader_Serialized(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	out := newLROut()
	l := NewLineReader(pr, out)
	a := askAsync(l, context.Background(), "A? ")
	out.waitFor(t, "A? ")
	b := askAsync(l, context.Background(), "B? ")

	time.Sleep(50 * time.Millisecond)
	if strings.Contains(out.String(), "B? ") {
		t.Fatalf("second prompt printed while first is pending: %q", out.String())
	}
	if _, err := pw.Write([]byte("1\n")); err != nil {
		t.Fatal(err)
	}
	if r := recvResult(t, a); r.line != "1" || r.err != nil {
		t.Fatalf("A got %q, %v", r.line, r.err)
	}
	out.waitFor(t, "B? ")
	if _, err := pw.Write([]byte("2\n")); err != nil {
		t.Fatal(err)
	}
	if r := recvResult(t, b); r.line != "2" || r.err != nil {
		t.Fatalf("B got %q, %v", r.line, r.err)
	}
}

func TestLineReader_EOF(t *testing.T) {
	pr, pw := io.Pipe()
	out := newLROut()
	l := NewLineReader(pr, out)
	a := askAsync(l, context.Background(), "A? ")
	out.waitFor(t, "A? ")
	pw.Close()
	if r := recvResult(t, a); r.err != io.EOF {
		t.Fatalf("pending Ask err = %v, want io.EOF", r.err)
	}
	b := askAsync(l, context.Background(), "B? ")
	if r := recvResult(t, b); r.err != io.EOF {
		t.Fatalf("later Ask err = %v, want io.EOF", r.err)
	}
}
