package providers

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jasonli0226/jas/internal/assert"
)

func drain(t *testing.T, out <-chan []byte, errc <-chan error) ([]string, error) {
	t.Helper()
	var got []string
	for p := range out {
		got = append(got, string(p))
	}
	return got, <-errc
}

func TestReadSSE_StopsAtDone(t *testing.T) {
	in := "data: one\n\ndata: two\n\ndata: three\n\ndata: [DONE]\n\ndata: ignored\n\n"
	out, errc := ReadSSE(context.Background(), strings.NewReader(in), time.Second)
	got, err := drain(t, out, errc)
	assert.NoErr(t, err)
	assert.Eq(t, len(got), 3)
	assert.Eq(t, got[0], "one")
	assert.Eq(t, got[2], "three")
}

func TestReadSSE_IgnoresCommentsAndBlankLines(t *testing.T) {
	in := ": keep-alive\n\ndata: a\r\n\n: another\ndata:b\n\n"
	out, errc := ReadSSE(context.Background(), strings.NewReader(in), time.Second)
	got, err := drain(t, out, errc)
	assert.NoErr(t, err)
	assert.Eq(t, len(got), 2)
	assert.Eq(t, got[0], "a")
	assert.Eq(t, got[1], "b")
}

func TestReadSSE_IdleTimeout(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	const idle = 100 * time.Millisecond
	start := time.Now()
	out, errc := ReadSSE(context.Background(), pr, idle)
	_, err := drain(t, out, errc)
	if !errors.Is(err, ErrIdleTimeout) {
		t.Fatalf("got %v, want ErrIdleTimeout", err)
	}
	if d := time.Since(start); d > idle+100*time.Millisecond {
		t.Fatalf("timeout took %v, want <= %v", d, idle+100*time.Millisecond)
	}
}

func TestReadSSE_SlowLongLineDoesNotTimeOut(t *testing.T) {
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		io.WriteString(pw, "data: ")
		for i := 0; i < 6; i++ {
			time.Sleep(40 * time.Millisecond) // total > idle, each gap < idle
			io.WriteString(pw, "xx")
		}
		io.WriteString(pw, "\n\n")
	}()
	out, errc := ReadSSE(context.Background(), pr, 100*time.Millisecond)
	got, err := drain(t, out, errc)
	assert.NoErr(t, err)
	assert.Eq(t, len(got), 1)
	assert.Eq(t, got[0], strings.Repeat("xx", 6))
}

func TestReadSSE_ContextCancel(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	ctx, cancel := context.WithCancel(context.Background())
	out, errc := ReadSSE(ctx, pr, time.Second)
	cancel()
	_, err := drain(t, out, errc)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}
