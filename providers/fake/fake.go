// Package fake is a scripted providers.Provider for tests.
package fake

import (
	"context"
	"fmt"
	"sync"

	"github.com/jasonli0226/jas/providers"
)

// Call is one scripted tool call.
type Call struct {
	Name string
	Args string // raw JSON arguments
}

// Turn is one scripted provider response.
type Turn struct {
	Reasoning string // emitted as its own event before Text
	Text      string
	Calls     []Call
	Usage     providers.Usage
	Err       error // returned instead of finishing the stream
	// OnEmit, if set, runs after Text/Calls were delivered and before Block waits.
	OnEmit func()
	Block  bool // after emitting Text/Calls, wait for ctx cancel and fail with ctx.Err()
}

// Provider replays Turns in order and records each Request it receives.
type Provider struct {
	Turns []Turn

	mu       sync.Mutex
	next     int
	Requests []providers.Request
}

var _ providers.Provider = (*Provider)(nil)

// Stream implements providers.Provider.
func (p *Provider) Stream(ctx context.Context, req providers.Request) (<-chan providers.StreamEvent, <-chan error) {
	out := make(chan providers.StreamEvent)
	errc := make(chan error, 1)

	p.mu.Lock()
	p.Requests = append(p.Requests, req)
	i := p.next
	p.next++
	p.mu.Unlock()

	go func() {
		defer close(out)
		defer close(errc)
		if i >= len(p.Turns) {
			errc <- fmt.Errorf("fake provider: no scripted turn %d", i)
			return
		}
		if err := p.play(ctx, i, p.Turns[i], out); err != nil {
			errc <- err
		}
	}()
	return out, errc
}

func (p *Provider) play(ctx context.Context, i int, t Turn, out chan<- providers.StreamEvent) error {
	if t.Err != nil {
		return t.Err
	}
	send := func(ev providers.StreamEvent) error {
		select {
		case out <- ev:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if t.Reasoning != "" {
		if err := send(providers.StreamEvent{Reasoning: t.Reasoning}); err != nil {
			return err
		}
	}
	if t.Text != "" {
		if err := send(providers.StreamEvent{Text: t.Text}); err != nil {
			return err
		}
	}
	for n, c := range t.Calls {
		d := &providers.ToolCallDelta{Index: n, ID: fmt.Sprintf("call_%d_%d", i, n), Name: c.Name, ArgsFragment: c.Args}
		if err := send(providers.StreamEvent{ToolCallDelta: d}); err != nil {
			return err
		}
	}
	if t.OnEmit != nil {
		t.OnEmit()
	}
	if t.Block {
		<-ctx.Done()
		return ctx.Err()
	}
	u := t.Usage
	reason := "stop"
	if len(t.Calls) > 0 {
		reason = "tool_calls"
	}
	if err := send(providers.StreamEvent{Usage: &u}); err != nil {
		return err
	}
	return send(providers.StreamEvent{FinishReason: reason})
}
