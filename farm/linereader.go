package farm

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
)

// ignoredNote is printed for a line read while no prompt is current.
const ignoredNote = "(ignored: no question is waiting)"

// LineReader reads one input (stdin) on a single goroutine for the farm's
// lifetime; prompts take lines from it instead of reading the input.
type LineReader struct {
	out io.Writer

	askMu chan struct{} // serializes Ask; holds one token while a prompt is on screen

	mu  sync.Mutex
	cur chan string // the current prompt's answer slot; nil when none
	eof bool

	outMu sync.Mutex
	done  chan struct{} // closed at EOF or a read error
}

// NewLineReader starts the one goroutine that reads in, and prints prompts
// and notes to out.
func NewLineReader(in io.Reader, out io.Writer) *LineReader {
	l := &LineReader{
		out:   out,
		askMu: make(chan struct{}, 1),
		done:  make(chan struct{}),
	}
	go l.read(in)
	return l
}

func (l *LineReader) read(in io.Reader) {
	r := bufio.NewReader(in)
	for {
		s, err := r.ReadString('\n')
		if s != "" {
			l.deliver(strings.TrimRight(s, "\r\n"))
		}
		if err != nil {
			l.mu.Lock()
			l.eof = true
			l.mu.Unlock()
			close(l.done)
			return
		}
	}
}

// deliver hands line to the current prompt, or drops it with a note.
func (l *LineReader) deliver(line string) {
	l.mu.Lock()
	ch := l.cur
	l.cur = nil
	l.mu.Unlock()
	if ch == nil {
		l.Note(ignoredNote)
		return
	}
	ch <- line // buffered (1) and the slot is taken only once
}

// Ask prints prompt and returns the next line read while this prompt is the
// current one. ctx done: returns ctx.Err() and the prompt stops being current.
// Asks are serialized (one prompt on screen at a time).
func (l *LineReader) Ask(ctx context.Context, prompt string) (string, error) {
	select {
	case l.askMu <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	case <-l.done:
		return "", io.EOF
	}
	defer func() { <-l.askMu }()

	ch := make(chan string, 1)
	l.mu.Lock()
	if l.eof {
		l.mu.Unlock()
		return "", io.EOF
	}
	l.cur = ch
	l.mu.Unlock()

	l.print(prompt)

	select {
	case line := <-ch:
		return line, nil
	case <-ctx.Done():
		l.release(ch)
		return "", ctx.Err()
	case <-l.done:
		l.release(ch)
		select {
		case line := <-ch: // a final unterminated line beat the EOF
			return line, nil
		default:
			return "", io.EOF
		}
	}
}

// release stops ch being the current prompt.
func (l *LineReader) release(ch chan string) {
	l.mu.Lock()
	if l.cur == ch {
		l.cur = nil
	}
	l.mu.Unlock()
}

// Note prints a line under the prompt lock (e.g. a withdrawal line).
func (l *LineReader) Note(line string) {
	l.print(line + "\n")
}

func (l *LineReader) print(s string) {
	l.outMu.Lock()
	defer l.outMu.Unlock()
	fmt.Fprint(l.out, s)
}
