package farm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/internal/testutil"
	"github.com/jasonli0226/jas/providers/fake"
)

// attachWait bounds every wait in these tests, so a regression fails
// instead of hanging.
const attachWait = 15 * time.Second

// shortTemp is a short dir under /tmp: a Unix socket path must fit ~104
// bytes, and t.TempDir paths (or TMPDIR, which newRepo isolates) often do
// not. Farms serve no socket on Windows, so these tests skip there.
func shortTemp(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("no attach socket on windows")
	}
	dir, err := os.MkdirTemp("/tmp", "jas")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// shortHome points the rig's jas home at a shortTemp dir.
func shortHome(t *testing.T, r *runRig) {
	t.Helper()
	dir := shortTemp(t)
	r.home, r.opts.Home = dir, dir
}

// heldRig is part a in a role that gates shell, whose shell turn holds
// until release is closed (signalling on started), with an unattended
// broker holding gated calls for wait (0: only while a client is present).
func heldRig(t *testing.T, wait time.Duration) (*runRig, *Broker, chan struct{}, chan struct{}) {
	t.Helper()
	started, release := make(chan struct{}, 1), make(chan struct{})
	parts := []Part{{ID: "a", Role: "gated", Goal: "a", Owns: []string{"a/**"}}}
	r := newRunRig(t, "true", parts, func(WorkerSession) []fake.Turn {
		hold := call("shell", `{"command":"echo hi"}`)
		hold.OnEmit = func() {
			started <- struct{}{}
			<-release
		}
		return []fake.Turn{hold, final("a done")}
	})
	r.opts.Cfg.Roles = map[string]RoleDef{"gated": {Tools: builtinTools(), Approve: "shell"}}
	b := NewBroker(BrokerOptions{Level: core.Never, Wait: wait})
	r.opts.Broker = b
	shortHome(t, r)
	return r, b, started, release
}

// attachClient is a test client of a farm's socket.
type attachClient struct {
	t    *testing.T
	conn net.Conn
	msgs chan AttachMessage // closed at EOF
	seq  int
	held []AttachNotice // notices read while waiting for a reply
}

func dialAttach(t *testing.T, home, id string) *attachClient {
	t.Helper()
	conn, err := net.Dial("unix", SockPath(home, id))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	c := &attachClient{t: t, conn: conn, msgs: make(chan AttachMessage, 64)}
	go func() {
		defer close(c.msgs)
		sc := bufio.NewScanner(conn)
		sc.Buffer(make([]byte, 64*1024), 4<<20)
		for sc.Scan() {
			var m AttachMessage
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Errorf("bad server line %q: %v", sc.Text(), err)
				return
			}
			c.msgs <- m
		}
	}()
	t.Cleanup(func() { _ = conn.Close() })
	return c
}

func (c *attachClient) next() (AttachMessage, bool) {
	c.t.Helper()
	select {
	case m, ok := <-c.msgs:
		return m, ok
	case <-time.After(attachWait):
		c.t.Fatal("timed out waiting for a server message")
	}
	return AttachMessage{}, false
}

// do sends req (with a fresh id) and returns its reply, keeping notices
// read meanwhile for notice.
func (c *attachClient) do(req AttachRequest) AttachReply {
	c.t.Helper()
	c.seq++
	req.ID = "r" + strconv.Itoa(c.seq)
	line, _ := json.Marshal(req)
	if _, err := c.conn.Write(append(line, '\n')); err != nil {
		c.t.Fatalf("write: %v", err)
	}
	for {
		m, ok := c.next()
		if !ok {
			c.t.Fatalf("connection closed before the reply to %s", req.Cmd)
		}
		if m.Notice != nil {
			c.held = append(c.held, *m.Notice)
			continue
		}
		if m.Reply == nil || m.Reply.ID != req.ID {
			c.t.Fatalf("unexpected message %+v waiting for %s", m, req.ID)
		}
		return *m.Reply
	}
}

func (c *attachClient) hello() AttachReply {
	c.t.Helper()
	rep := c.do(AttachRequest{Cmd: "hello"})
	if !rep.OK {
		c.t.Fatalf("hello: %+v", rep)
	}
	return rep
}

// notice returns the next notice of type typ, skipping others.
func (c *attachClient) notice(typ string) AttachNotice {
	c.t.Helper()
	for len(c.held) > 0 {
		n := c.held[0]
		c.held = c.held[1:]
		if n.Type == typ {
			return n
		}
	}
	for {
		m, ok := c.next()
		if !ok {
			c.t.Fatalf("connection closed before a %s notice", typ)
		}
		if m.Notice != nil && m.Notice.Type == typ {
			return *m.Notice
		}
	}
}

// eof waits for the server to close the connection.
func (c *attachClient) eof() {
	c.t.Helper()
	for {
		if _, ok := c.next(); !ok {
			return
		}
	}
}

func waitDone(t *testing.T, h *Handle) Result {
	t.Helper()
	select {
	case <-h.Done():
	case <-time.After(attachWait):
		h.Stop(true)
		t.Fatal("the farm did not end")
	}
	return h.Wait()
}

func waitPending(t *testing.T, b *Broker) ApprovalRequest {
	t.Helper()
	deadline := time.Now().Add(attachWait)
	for time.Now().Before(deadline) {
		if p := b.Pending(); len(p) > 0 {
			return p[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no pending approval")
	return ApprovalRequest{}
}

func waitStarted(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(attachWait):
		t.Fatal("the part never started")
	}
}

// attachDecisions is "part choice" for every owner_decision from attach.
func attachDecisions(r *runRig) []string {
	var out []string
	for _, ev := range r.events(EvOwnerDecision) {
		if ev["source"] == "attach" {
			out = append(out, strings.TrimSpace(asString(ev["part"])+" "+asString(ev["choice"])))
		}
	}
	return out
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func partClosedBy(t *testing.T, r *runRig, part string) string {
	t.Helper()
	for _, e := range testutil.ReadEvents(t, filepath.Join(r.farmDir(), "parts", part, "events.jsonl")) {
		if e["type"] == EvApprovalClosed {
			if d, ok := e["data"].(map[string]any); ok {
				return asString(d["by"])
			}
			return asString(e["by"])
		}
	}
	return ""
}

// heldWriters is twoParts where a holds on its first turn.
func heldWriters(t *testing.T) (*runRig, *provRig, chan struct{}, chan struct{}) {
	t.Helper()
	started, release := make(chan struct{}, 1), make(chan struct{})
	pr := &provRig{}
	r := newRunRig(t, "true", twoParts(), twoWriters)
	pr.wrap(r, func(ws WorkerSession) []fake.Turn {
		if ws.Part.ID == "a" {
			return gated(started, release)
		}
		return twoWriters(ws)
	})
	shortHome(t, r)
	return r, pr, started, release
}

func TestAttachServer_SocketMode(t *testing.T) {
	r, _, started, release := heldWriters(t)
	h := start(t, r)
	waitStarted(t, started)
	fi, err := os.Stat(SockPath(r.home, h.FarmID()))
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	close(release)
	if fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v", fi.Mode())
	}
	waitDone(t, h)
}

func TestAttachServer_Hello(t *testing.T) {
	r, b, started, release := heldRig(t, time.Minute)
	h := start(t, r)
	waitStarted(t, started)
	close(release)
	req := waitPending(t, b)
	c := dialAttach(t, r.home, h.FarmID())
	rep := c.hello()
	if rep.Protocol != AttachProtocol || rep.FarmID != h.FarmID() || rep.Client != "attach:c1" {
		t.Fatalf("hello %+v", rep)
	}
	if len(rep.Pending) != 1 || rep.Pending[0].ID != req.ID || rep.Pending[0].Tool != "shell" || rep.Pending[0].Part != "a" {
		t.Fatalf("pending %+v", rep.Pending)
	}
	if rep := c.do(AttachRequest{Cmd: "approve", ApprovalID: req.ID, Answer: "yes"}); !rep.OK {
		t.Fatalf("approve %+v", rep)
	}
	waitDone(t, h)
	if ev := r.last("attach_connected"); ev["client"] != "c1" {
		t.Fatalf("attach_connected %v", ev)
	}
}

func TestAttachServer_Steer(t *testing.T) {
	r, pr, started, release := heldWriters(t)
	h := start(t, r)
	waitStarted(t, started)
	c := dialAttach(t, r.home, h.FarmID())
	c.hello()
	rep := c.do(AttachRequest{Cmd: "steer", Part: "a", Text: "use slog"})
	close(release)
	if !rep.OK {
		t.Fatalf("steer %+v", rep)
	}
	if res := waitDone(t, h); res.Code != 0 {
		t.Fatalf("result %+v", res)
	}
	reqs := pr.requests("a")
	if len(reqs) < 2 || !strings.Contains(requestText(reqs[len(reqs)-1]), "use slog") {
		t.Fatal("the steer did not reach part a's next request")
	}
	if got := strings.Join(attachDecisions(r), ","); got != "a steer" {
		t.Fatalf("attach decisions %q", got)
	}
}

func TestAttachServer_SteerNotRunning(t *testing.T) {
	r, _, started, release := heldWriters(t)
	h := start(t, r)
	waitStarted(t, started)
	c := dialAttach(t, r.home, h.FarmID())
	c.hello()
	rep := c.do(AttachRequest{Cmd: "steer", Part: "zz", Text: "x"})
	for _, cmd := range []string{"pause", "resume", "drop"} {
		if rep := c.do(AttachRequest{Cmd: cmd, Part: "zz"}); rep.OK || rep.Error != `unknown part "zz"` {
			t.Errorf("%s zz %+v", cmd, rep)
		}
	}
	close(release)
	if rep.OK || rep.Error != `unknown part "zz"` {
		t.Fatalf("steer zz %+v", rep)
	}
	waitDone(t, h)
	if got := attachDecisions(r); len(got) != 0 {
		t.Fatalf("a refused steer logged %v", got)
	}
}

func TestAttachServer_PauseResumeDrop(t *testing.T) {
	r, _, started, release := heldWriters(t)
	h := start(t, r)
	waitStarted(t, started)
	c := dialAttach(t, r.home, h.FarmID())
	c.hello()
	for _, cmd := range []string{"pause", "resume", "drop"} {
		if rep := c.do(AttachRequest{Cmd: cmd, Part: "a"}); !rep.OK {
			close(release)
			t.Fatalf("%s %+v", cmd, rep)
		}
	}
	close(release)
	if res := waitDone(t, h); res.Reason != ReasonPartDropped {
		t.Fatalf("result %+v", res)
	}
	if got := strings.Join(attachDecisions(r), ","); got != "a pause,a resume,a drop" {
		t.Fatalf("attach decisions %q", got)
	}
	for _, ev := range r.events(EvOwnerDecision) {
		if ev["source"] == "owner" && ev["part"] == "a" {
			t.Fatalf("attach command also logged as the owner's: %v", ev)
		}
	}
}

func TestAttachServer_Approve(t *testing.T) {
	r, b, started, release := heldRig(t, time.Minute)
	h := start(t, r)
	waitStarted(t, started)
	c1 := dialAttach(t, r.home, h.FarmID())
	c2 := dialAttach(t, r.home, h.FarmID())
	c1.hello()
	c2.hello()
	close(release)
	req := waitPending(t, b)
	if rep := c1.do(AttachRequest{Cmd: "approve", ApprovalID: req.ID, Answer: "yes"}); !rep.OK {
		t.Fatalf("c1 approve %+v", rep)
	}
	rep := c2.do(AttachRequest{Cmd: "approve", ApprovalID: req.ID, Answer: "no"})
	if rep.OK || rep.Error != "already answered (yes, by attach:c1)" {
		t.Fatalf("c2 approve %+v", rep)
	}
	if rep := c2.do(AttachRequest{Cmd: "approve", ApprovalID: "a99", Answer: "yes"}); rep.OK || rep.Error != "unknown approval" {
		t.Fatalf("unknown id %+v", rep)
	}
	if rep := c2.do(AttachRequest{Cmd: "approve", ApprovalID: req.ID, Answer: "maybe"}); rep.OK || rep.Error == "" {
		t.Fatalf("bad answer %+v", rep)
	}
	waitDone(t, h)
	if ps := h.Snapshot().Parts["a"]; ps.Status != StatusDone {
		t.Fatalf("part a %+v", ps)
	}
	if got := strings.Join(attachDecisions(r), ","); got != "a approve:yes" {
		t.Fatalf("attach decisions %q", got)
	}
}

// A farm started without a Broker gets the default unattended one, so an
// approve names an approval it does not have.
func TestAttachServer_ApproveWithDefaultBroker(t *testing.T) {
	r, _, started, release := heldWriters(t)
	h := start(t, r)
	waitStarted(t, started)
	c := dialAttach(t, r.home, h.FarmID())
	if rep := c.hello(); len(rep.Pending) != 0 {
		t.Fatalf("hello %+v", rep)
	}
	rep := c.do(AttachRequest{Cmd: "approve", ApprovalID: "a1", Answer: "yes"})
	close(release)
	if rep.OK || rep.Error != "unknown approval" {
		t.Fatalf("approve %+v", rep)
	}
	waitDone(t, h)
}

func TestAttachServer_Notices(t *testing.T) {
	r, _, started, release := heldRig(t, 0)
	h := start(t, r)
	waitStarted(t, started)
	c := dialAttach(t, r.home, h.FarmID())
	c.hello()
	close(release)
	n := c.notice(EvApprovalRequested)
	if n.Request == nil || n.Request.Part != "a" || n.Request.Tool != "shell" {
		t.Fatalf("requested %+v", n)
	}
	if rep := c.do(AttachRequest{Cmd: "approve", ApprovalID: n.Request.ID, Answer: "y"}); !rep.OK {
		t.Fatalf("approve %+v", rep)
	}
	cl := c.notice(EvApprovalClosed)
	if cl.Request == nil || cl.Request.ID != n.Request.ID || cl.Answer != "yes" || cl.By != "attach:c1" {
		t.Fatalf("closed %+v", cl)
	}
	waitDone(t, h)
}

func TestAttachServer_GrantNotice(t *testing.T) {
	r, _, started, release := heldRig(t, 0)
	h := start(t, r)
	waitStarted(t, started)
	c := dialAttach(t, r.home, h.FarmID())
	c.hello()
	close(release)
	n := c.notice(EvApprovalRequested)
	if rep := c.do(AttachRequest{Cmd: "approve", ApprovalID: n.Request.ID, Answer: "farm"}); !rep.OK {
		t.Fatalf("approve %+v", rep)
	}
	g := c.notice(NoticeApprovalGranted)
	if g.Part != "a" || g.Text != "[a] always: shell: echo hi (covers any arguments)" {
		t.Fatalf("granted %+v", g)
	}
	waitDone(t, h)
}

func TestAttachServer_PresenceNeedsHello(t *testing.T) {
	r, _, started, release := heldRig(t, 0)
	h := start(t, r)
	waitStarted(t, started)
	c := dialAttach(t, r.home, h.FarmID())
	for _, req := range []AttachRequest{{Cmd: "steer", Part: "a", Text: "x"}, {Cmd: "stop"}, {Cmd: "approve", Answer: "yes"}} {
		if rep := c.do(req); rep.OK || rep.Error != "hello first" {
			t.Fatalf("%s before hello %+v", req.Cmd, rep)
		}
	}
	close(release)
	waitDone(t, h)
	if by := partClosedBy(t, r, "a"); by != byNoClient {
		t.Fatalf("approval closed by %q, want no_client", by)
	}
	if evs := r.events("attach_connected"); len(evs) != 0 {
		t.Fatalf("attach_connected without hello: %v", evs)
	}
}

func TestAttachServer_DisconnectReleasesHold(t *testing.T) {
	r, _, started, release := heldRig(t, 0)
	h := start(t, r)
	waitStarted(t, started)
	c := dialAttach(t, r.home, h.FarmID())
	c.hello()
	close(release)
	c.notice(EvApprovalRequested) // held while c is present
	_ = c.conn.Close()
	waitDone(t, h)
	if by := partClosedBy(t, r, "a"); by != byNoClient {
		t.Fatalf("approval closed by %q, want no_client", by)
	}
	if ev := r.last("attach_disconnected"); ev["client"] != "c1" {
		t.Fatalf("attach_disconnected %v", ev)
	}
}

func TestAttachServer_SteerUndeliveredToSender(t *testing.T) {
	// A steer that arrives while the supervisor runs part_check (after the
	// worker's final answer) is never read, so it goes back to its sender.
	r := newRunRig(t, "true", twoParts(), twoWriters)
	shortHome(t, r)
	r.opts.Cfg.PartCheck = "true"
	w := &undeliveredWatcher{}
	var c atomic.Pointer[attachClient]
	steered := make(chan AttachReply, 1)
	w.onFarm = func(typ string, m map[string]any) {
		if typ == EvPartCheck && m["part"] == "a" {
			for c.Load() == nil {
				time.Sleep(time.Millisecond)
			}
			steered <- c.Load().do(AttachRequest{Cmd: "steer", Part: "a", Text: "late"})
		}
	}
	r.opts.Watch = w
	h := start(t, r)
	cl := dialAttach(t, r.home, h.FarmID())
	cl.hello()
	c.Store(cl)
	var rep AttachReply
	select {
	case rep = <-steered:
	case <-time.After(attachWait):
		t.Fatal("never steered")
	}
	if !rep.OK {
		t.Fatalf("steer %+v", rep)
	}
	n := cl.notice("steer_undelivered")
	if n.Part != "a" || strings.Join(n.Texts, "|") != "late" {
		t.Fatalf("notice %+v", n)
	}
	waitDone(t, h)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.part != "" || len(w.texts) != 0 {
		t.Fatalf("host watcher handed %q %v", w.part, w.texts)
	}
}

func TestAttachServer_SteerUndeliveredClientGone(t *testing.T) {
	// The sender left: the host watcher gets the text, as today.
	r := newRunRig(t, "true", twoParts(), twoWriters)
	shortHome(t, r)
	r.opts.Cfg.PartCheck = "true"
	w := &undeliveredWatcher{}
	var home, id atomic.Value
	w.onFarm = func(typ string, m map[string]any) {
		if typ == EvPartCheck && m["part"] == "a" {
			for id.Load() == nil {
				time.Sleep(time.Millisecond)
			}
			cl := dialAttach(t, home.Load().(string), id.Load().(string))
			cl.hello()
			if rep := cl.do(AttachRequest{Cmd: "steer", Part: "a", Text: "late"}); !rep.OK {
				t.Errorf("steer %+v", rep)
			}
			_ = cl.conn.Close()
			cl.eof()
		}
	}
	r.opts.Watch = w
	home.Store(r.home)
	h := start(t, r)
	id.Store(h.FarmID())
	waitDone(t, h)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.part != "a" || strings.Join(w.texts, "|") != "late" {
		t.Fatalf("host watcher handed %q %v", w.part, w.texts)
	}
}

func TestAttachServer_BadInput(t *testing.T) {
	r, _, started, release := heldWriters(t)
	h := start(t, r)
	waitStarted(t, started)
	c := dialAttach(t, r.home, h.FarmID())
	if _, err := c.conn.Write([]byte("not json\n")); err != nil {
		t.Fatal(err)
	}
	m, ok := c.next()
	if !ok || m.Reply == nil || m.Reply.OK || m.Reply.Error == "" {
		t.Fatalf("bad JSON got %+v", m)
	}
	c.hello()
	if rep := c.do(AttachRequest{Cmd: "dance"}); rep.OK || !strings.Contains(rep.Error, "unknown command") {
		t.Fatalf("unknown command %+v", rep)
	}
	go func() { _, _ = c.conn.Write([]byte(strings.Repeat("x", 2<<20) + "\n")) }()
	c.eof()
	c2 := dialAttach(t, r.home, h.FarmID())
	c2.hello()
	close(release)
	if res := waitDone(t, h); res.Code != 0 {
		t.Fatalf("result %+v", res)
	}
}

func TestAttachServer_RemovedOnEnd(t *testing.T) {
	r, _, started, release := heldWriters(t)
	h := start(t, r)
	waitStarted(t, started)
	c := dialAttach(t, r.home, h.FarmID())
	c.hello()
	close(release)
	waitDone(t, h)
	if _, err := os.Stat(SockPath(r.home, h.FarmID())); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket after end: %v", err)
	}
	c.eof()
	if evs := r.events(EvFarmEnded); len(evs) != 1 {
		t.Fatalf("farm_ended %v", evs)
	}
}

func TestAttachServer_StaleSocket(t *testing.T) {
	path := filepath.Join(shortTemp(t), "f.sock")
	old, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	old.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = old.Close() // leaves the socket file, with nothing listening
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stale socket not left: %v", err)
	}
	ln, err := listenAttach(path)
	if err != nil {
		t.Fatalf("listen over a stale socket: %v", err)
	}
	defer ln.Close()
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if c, err := ln.Accept(); err == nil {
			_ = c.Close()
		}
	}()
	conn, err := net.Dial("unix", path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()
	wg.Wait()
}

func TestAttachServer_UnavailableNonFatal(t *testing.T) {
	r := newRunRig(t, "true", twoParts(), twoWriters)
	shortHome(t, r)
	long := filepath.Join(r.home, strings.Repeat("h", 120))
	r.home, r.opts.Home = long, long
	code, _ := r.run(context.Background())
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if ev := r.last("attach_unavailable"); asString(ev["error"]) == "" {
		t.Fatalf("attach_unavailable %v", ev)
	}
}

func TestAttachServer_SlowClientDoesNotBlockEnd(t *testing.T) {
	// A client that sends without ever reading fills its socket; the farm
	// must still end.
	r, _, started, release := heldWriters(t)
	h := start(t, r)
	waitStarted(t, started)
	conn, err := net.Dial("unix", SockPath(r.home, h.FarmID()))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	line := []byte(`{"id":"x","cmd":"steer","part":"zz","text":"` + strings.Repeat("y", 64) + `"}` + "\n")
	flood := []byte(strings.Repeat(string(line), 20000))
	go func() { _, _ = conn.Write(flood) }()
	close(release)
	waitDone(t, h)
}
