package farm

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"time"
)

// Farm events the attach server logs to farm.jsonl.
const (
	EvAttachConnected    = "attach_connected"    // a client said hello
	EvAttachDisconnected = "attach_disconnected" // a client that said hello left
	EvAttachUnavailable  = "attach_unavailable"  // the socket could not be served; attach is watch-only
)

const (
	attachSockMode   = 0o600
	attachMaxLine    = 1 << 20 // longest request line; longer closes the connection
	attachLineBuf    = 64 * 1024
	attachOutBuffer  = 256 // per-connection outgoing messages; full closes it
	attachFlushWait  = time.Second
	attachSource     = "attach"
	attachClientName = "c"
)

// errNotRunningText is the reply for a command on a part with no live
// session.
const errNotRunningText = "part not running"

// errHelloFirstText refuses a command sent before hello: a client must be
// present (and get its notices) before it changes the farm.
const errHelloFirstText = "hello first"

// attachServer serves a running farm's attach socket. It also wraps the
// host's Watcher, so steer texts a client sent that a part never read go
// back to that client rather than to the host.
type attachServer struct {
	inner Watcher // the host's watcher; nil: none

	h    *Handle
	ln   net.Listener
	path string
	wg   sync.WaitGroup

	mu       sync.Mutex
	conns    map[*attachConn]struct{}
	seq      int
	present  int // connections that said hello
	steers   []attachSteer
	stopping bool
}

// attachSteer is a steer text the server queued, by its sender.
type attachSteer struct {
	part, text string
	c          *attachConn
}

// attachConn is one client connection.
type attachConn struct {
	name  string
	conn  net.Conn
	out   chan AttachMessage
	done  chan struct{}
	once  sync.Once
	hello bool   // guarded by attachServer.mu
	unsub func() // the broker subscription, after hello
}

func newAttachServer(inner Watcher) *attachServer {
	return &attachServer{inner: inner, conns: map[*attachConn]struct{}{}}
}

// listenAttach listens on a Unix socket at path, mode 0600, replacing a
// leftover socket file (the farm's lock is held, so nobody else serves it).
func listenAttach(path string) (net.Listener, error) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, attachSockMode); err != nil {
		_ = ln.Close()
		return nil, err
	}
	return ln, nil
}

// start serves h's socket. A failure is not fatal: the farm runs on, the
// failure is logged as attach_unavailable, and attach stays watch-only.
// Windows serves no socket.
func (s *attachServer) start(h *Handle, home string) {
	s.h = h
	if runtime.GOOS == "windows" {
		return
	}
	s.path = sockPath(home, h.FarmID())
	ln, err := listenAttach(s.path)
	if err != nil {
		s.event(EvAttachUnavailable, map[string]any{"error": err.Error()})
		return
	}
	s.ln = ln
	s.wg.Add(1)
	go s.accept()
}

// stop closes the socket and every connection and waits for them. Clients
// cut off by the farm's end are not logged as disconnected: farm_ended says
// it.
func (s *attachServer) stop() {
	if s.ln == nil {
		return
	}
	s.mu.Lock()
	s.stopping = true
	conns := make([]*attachConn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	_ = s.ln.Close()
	for _, c := range conns {
		c.close()
	}
	s.wg.Wait()
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		s.h.r.s.noteErr(fmt.Errorf("remove attach socket: %w", err))
	}
}

func (s *attachServer) event(typ string, m map[string]any) { s.h.r.s.event(typ, m) }

func (s *attachServer) accept() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return // closed by stop
		}
		s.mu.Lock()
		if s.stopping {
			s.mu.Unlock()
			_ = conn.Close()
			return
		}
		s.seq++
		c := &attachConn{
			name: attachClientName + strconv.Itoa(s.seq), conn: conn,
			out: make(chan AttachMessage, attachOutBuffer), done: make(chan struct{}),
		}
		s.conns[c] = struct{}{}
		s.wg.Add(2)
		s.mu.Unlock()
		go c.write(&s.wg)
		go s.serve(c)
	}
}

// close ends the connection: the writer flushes what is queued, then
// closes the socket, which ends the reader. The write deadline also
// unblocks a write already stuck on a client that does not read.
func (c *attachConn) close() {
	c.once.Do(func() {
		close(c.done)
		_ = c.conn.SetWriteDeadline(time.Now().Add(attachFlushWait))
	})
}

// send queues m and reports whether it did; a client too slow to drain its
// buffer is closed.
func (c *attachConn) send(m AttachMessage) bool {
	select {
	case <-c.done:
		return false
	default:
	}
	select {
	case c.out <- m:
		return true
	default:
		c.close()
		return false
	}
}

func (c *attachConn) write(wg *sync.WaitGroup) {
	defer wg.Done()
	defer c.conn.Close()
	enc := json.NewEncoder(c.conn) // one line per message
	for {
		select {
		case m := <-c.out:
			if enc.Encode(m) != nil {
				c.close()
				return
			}
		case <-c.done:
			for {
				select {
				case m := <-c.out:
					if enc.Encode(m) != nil {
						return
					}
				default:
					return
				}
			}
		}
	}
}

// serve reads c's requests until it closes, errs or sends a line over
// attachMaxLine.
func (s *attachServer) serve(c *attachConn) {
	defer s.wg.Done()
	defer s.left(c)
	sc := bufio.NewScanner(c.conn)
	sc.Buffer(make([]byte, attachLineBuf), attachMaxLine)
	for sc.Scan() {
		select {
		case <-c.done: // closed (too slow, or the farm is ending)
			return
		default:
		}
		var req AttachRequest
		if err := json.Unmarshal(sc.Bytes(), &req); err != nil {
			c.send(AttachMessage{Reply: &AttachReply{Error: "bad request: " + err.Error()}})
			continue
		}
		s.handle(c, req)
	}
}

// left forgets c once its reader ends; a client that said hello no longer
// counts as present.
func (s *attachServer) left(c *attachConn) {
	c.close()
	s.mu.Lock()
	delete(s.conns, c)
	was, stopping, unsub := c.hello, s.stopping, c.unsub
	if was {
		c.hello = false
		s.present--
		if b := s.h.Broker(); b != nil {
			b.SetClients(s.present)
		}
	}
	s.mu.Unlock()
	if unsub != nil {
		unsub()
	}
	if was && !stopping {
		s.event(EvAttachDisconnected, map[string]any{"client": c.name})
	}
}

func (s *attachServer) handle(c *attachConn, req AttachRequest) {
	rep := AttachReply{ID: req.ID}
	s.mu.Lock()
	said := c.hello
	s.mu.Unlock()
	if !said && req.Cmd != AttachHello {
		rep.Error = errHelloFirstText
		c.send(AttachMessage{Reply: &rep})
		return
	}
	var err error
	switch req.Cmd {
	case AttachHello:
		s.hello(c, req)
		return
	case AttachSteer:
		err = s.steer(c, req.Part, req.Text)
	case AttachPause, AttachResume:
		if err = needPart(req.Part); err == nil {
			err = s.h.pauseResume(req.Part, req.Cmd == AttachPause, attachSource)
		}
	case AttachDrop:
		if err = needPart(req.Part); err == nil {
			err = s.h.drop(req.Part, attachSource)
		}
	case AttachStop:
		s.event(EvOwnerDecision, map[string]any{"choice": AttachStop, "source": attachSource, "hard": req.Hard})
		c.send(AttachMessage{Reply: &AttachReply{ID: req.ID, OK: true}})
		s.h.Stop(req.Hard)
		return
	case AttachApprove:
		err = s.approve(c, req.ApprovalID, req.Answer)
	default:
		err = fmt.Errorf("unknown command %q", req.Cmd)
	}
	switch {
	case errors.Is(err, ErrPartNotRunning):
		rep.Error = errNotRunningText
	case err != nil:
		rep.Error = err.Error()
	default:
		rep.OK = true
	}
	c.send(AttachMessage{Reply: &rep})
}

func needPart(part string) error {
	if part == "" {
		return errors.New("missing part")
	}
	return nil
}

// hello registers c's presence (once), replies with the pending approvals
// and then forwards the broker's notices to c.
func (s *attachServer) hello(c *attachConn, req AttachRequest) {
	rep := AttachReply{ID: req.ID, OK: true, FarmID: s.h.FarmID(), Protocol: AttachProtocol, Client: attachSource + ":" + c.name}
	b := s.h.Broker()
	s.mu.Lock()
	first := !c.hello
	var notices <-chan Notice
	if first {
		c.hello = true
		s.present++
		if b != nil {
			// Subscribe before reading Pending, so nothing opened in between
			// is missed (it may come twice: in the list and as a notice).
			notices, c.unsub = b.Subscribe()
			b.SetClients(s.present)
		}
	}
	s.mu.Unlock()
	if b != nil {
		rep.Pending = b.Pending()
	}
	c.send(AttachMessage{Reply: &rep})
	if !first {
		return
	}
	if notices != nil {
		s.wg.Add(1)
		go s.forward(c, notices)
	}
	s.event(EvAttachConnected, map[string]any{"client": c.name})
}

// forward sends broker notices to c until the subscription ends.
func (s *attachServer) forward(c *attachConn, notices <-chan Notice) {
	defer s.wg.Done()
	for n := range notices {
		req := n.Request
		c.send(AttachMessage{Notice: &AttachNotice{Type: n.Type, Request: &req, Answer: n.Answer, By: n.By, Part: req.Part, Text: n.Text}})
	}
}

// steer queues text for part, recording c as its sender first: the part
// may finish and hand the text back before Steer returns.
func (s *attachServer) steer(c *attachConn, part, text string) error {
	if err := needPart(part); err != nil {
		return err
	}
	if text == "" {
		return errors.New("missing text")
	}
	rec := attachSteer{part: part, text: text, c: c}
	s.mu.Lock()
	s.steers = append(s.steers, rec)
	s.mu.Unlock()
	if err := s.h.Steer(part, text); err != nil {
		s.mu.Lock()
		for i := len(s.steers) - 1; i >= 0; i-- {
			if s.steers[i] == rec {
				s.steers = slices.Delete(s.steers, i, i+1)
				break
			}
		}
		s.mu.Unlock()
		return err
	}
	s.event(EvOwnerDecision, map[string]any{"part": part, "choice": AttachSteer, "source": attachSource})
	return nil
}

func (s *attachServer) approve(c *attachConn, id, answer string) error {
	b := s.h.Broker()
	a, ok := ParseAnswer(answer)
	if !ok || answer == "" {
		return fmt.Errorf("bad answer %q (yes, no, always, farm)", answer)
	}
	part := ""
	for _, p := range b.Pending() {
		if p.ID == id {
			part = p.Part
		}
	}
	if ok, msg := b.Answer(id, a, attachSource+":"+c.name); !ok {
		return errors.New(msg)
	}
	s.event(EvOwnerDecision, map[string]any{
		"part": part, "choice": AttachApprove + ":" + a.String(), "source": attachSource, "approval_id": id,
	})
	return nil
}

var _ Watcher = (*attachServer)(nil)

// FarmEvent passes the event on. A part's end (or the integrator's) means
// every steer it did not read was already handed back, so the rest of its
// records were delivered and are dropped.
func (s *attachServer) FarmEvent(typ string, m map[string]any) {
	part, _ := m["part"].(string)
	switch typ {
	case EvIntegrateEnded:
		part = integratePart
	case EvReviewEnded:
		part = reviewPart // round 2 reuses the id, so round 1's leftovers must go
	case EvPartEnded:
	default:
		part = ""
	}
	if part != "" {
		s.dropSteers(part)
	}
	if s.inner != nil {
		s.inner.FarmEvent(typ, m)
	}
}

// dropSteers forgets the steer records addressed to part.
func (s *attachServer) dropSteers(part string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.steers[:0]
	for _, r := range s.steers {
		if r.part != part {
			kept = append(kept, r)
		}
	}
	clear(s.steers[len(kept):])
	s.steers = kept
}

// WorkerEvent passes the event on.
func (s *attachServer) WorkerEvent(part, typ string, m map[string]any) {
	if s.inner != nil {
		s.inner.WorkerEvent(part, typ, m)
	}
}

// SteerUndelivered sends each text a still-connected client sent back to
// that client (matched by value, oldest first) and hands the rest to the
// host's watcher.
func (s *attachServer) SteerUndelivered(part string, texts []string) {
	var rest []string
	byConn := map[*attachConn][]string{}
	var order []*attachConn
	s.mu.Lock()
	for _, t := range texts {
		i := s.findSteer(part, t)
		if i < 0 {
			rest = append(rest, t)
			continue
		}
		c := s.steers[i].c
		s.steers = slices.Delete(s.steers, i, i+1)
		if _, live := s.conns[c]; !live {
			rest = append(rest, t)
			continue
		}
		if _, seen := byConn[c]; !seen {
			order = append(order, c)
		}
		byConn[c] = append(byConn[c], t)
	}
	s.mu.Unlock()
	for _, c := range order {
		if !c.send(AttachMessage{Notice: &AttachNotice{Type: NoticeSteerUndelivered, Part: part, Texts: byConn[c]}}) {
			rest = append(rest, byConn[c]...) // the client left meanwhile
		}
	}
	if len(rest) > 0 && s.inner != nil {
		s.inner.SteerUndelivered(part, rest)
	}
}

// findSteer is the oldest record of text steered to part, or -1.
func (s *attachServer) findSteer(part, text string) int {
	for i, r := range s.steers {
		if r.part == part && r.text == text {
			return i
		}
	}
	return -1
}
