package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonli0226/jas/farm"
)

const ctlWait = 5 * time.Second

// fakeFarm speaks the attach protocol on the farm's socket.
type fakeFarm struct {
	ln      net.Listener
	reqs    chan farm.AttachRequest
	ready   chan struct{} // closed once a client connected
	once    sync.Once
	c       net.Conn
	release func() error // drops the farm's lock
}

// ctlHome is a short home dir (Unix socket paths are length-limited) with a
// locked farm fixture and a fake server on its socket.
func ctlHome(t *testing.T, id string, pending []farm.ApprovalRequest, reply func(farm.AttachRequest) *farm.AttachReply) (string, *fakeFarm) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("no attach socket on windows")
	}
	home, err := os.MkdirTemp("", "jas")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	fixtureFarm(t, home, id, []string{"p1"})
	release := lockFixture(t, home, id)
	if err := os.MkdirAll(filepath.Join(home, "run"), 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", farm.SockPath(home, id))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	ff := &fakeFarm{ln: ln, reqs: make(chan farm.AttachRequest, 16), ready: make(chan struct{}), release: release}
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		t.Cleanup(func() { _ = conn.Close() })
		ff.c = conn
		enc := json.NewEncoder(conn)
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			var req farm.AttachRequest
			if json.Unmarshal(sc.Bytes(), &req) != nil {
				continue
			}
			if req.Cmd == farm.AttachHello {
				_ = enc.Encode(farm.AttachMessage{Reply: &farm.AttachReply{ID: req.ID, OK: true, FarmID: id, Protocol: farm.AttachProtocol, Pending: pending}})
				ff.once.Do(func() { close(ff.ready) }) // after the hello reply
				continue
			}
			ff.reqs <- req
			rep := &farm.AttachReply{ID: req.ID, OK: true}
			if reply != nil {
				rep = reply(req)
				rep.ID = req.ID
			}
			_ = enc.Encode(farm.AttachMessage{Reply: rep})
		}
	}()
	return home, ff
}

func (ff *fakeFarm) next(t *testing.T) farm.AttachRequest {
	t.Helper()
	select {
	case r := <-ff.reqs:
		return r
	case <-time.After(ctlWait):
		t.Fatal("server received no request")
		return farm.AttachRequest{}
	}
}

func (ff *fakeFarm) push(t *testing.T, n farm.AttachNotice) {
	t.Helper()
	ff.waitClient(t)
	if err := json.NewEncoder(ff.c).Encode(farm.AttachMessage{Notice: &n}); err != nil {
		t.Fatal(err)
	}
}

func (ff *fakeFarm) waitClient(t *testing.T) {
	t.Helper()
	select {
	case <-ff.ready:
	case <-time.After(ctlWait):
		t.Fatal("no client connected")
	}
}

// ctlAttach runs attach with in as the command line.
func ctlAttach(t *testing.T, home string, in io.Reader) (*syncBuf, func() error) {
	t.Helper()
	out := &syncBuf{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- runAttach(ctx, home, nil, in, out, attachPoll) }()
	return out, func() error {
		select {
		case err := <-done:
			return err
		case <-time.After(ctlWait):
			t.Fatalf("attach did not return; output:\n%s", out.String())
			return nil
		}
	}
}

const ctlFarm = "20261122T010000-ctl"

// idleInput is a command input that never sends a line (a terminal nobody
// types in), so attach connects for control.
func idleInput(t *testing.T) io.Reader {
	t.Helper()
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	return pr
}

func TestAttach_ControlSteer(t *testing.T) {
	home, ff := ctlHome(t, ctlFarm, nil, nil)
	ctlAttach(t, home, strings.NewReader("steer p1 use slog\n"))
	r := ff.next(t)
	if r.Cmd != farm.AttachSteer || r.Part != "p1" || r.Text != "use slog" {
		t.Fatalf("got %+v", r)
	}
}

func TestAttach_ControlApproveDefaultID(t *testing.T) {
	pend := []farm.ApprovalRequest{{ID: "a1", Part: "backend-api", Tool: "shell", Summary: "npm install zod"}, {ID: "a2", Part: "p1", Tool: "shell", Summary: "ls"}}
	home, ff := ctlHome(t, ctlFarm, pend, nil)
	out, _ := ctlAttach(t, home, strings.NewReader("y\n"))
	r := ff.next(t)
	if r.Cmd != farm.AttachApprove || r.ApprovalID != "a1" || r.Answer != "y" {
		t.Fatalf("got %+v", r)
	}
	waitOutput(t, out, "[backend-api] approve [a1] shell: npm install zod (y/n/always/farm)")
}

func TestAttach_ControlApproveNoPending(t *testing.T) {
	home, ff := ctlHome(t, ctlFarm, nil, nil)
	out, _ := ctlAttach(t, home, strings.NewReader("always\nfarm a9\n"))
	waitOutput(t, out, "no pending approval")
	r := ff.next(t)
	if r.ApprovalID != "a9" || r.Answer != "farm" {
		t.Fatalf("got %+v", r)
	}
}

func TestAttach_ControlAlreadyAnswered(t *testing.T) {
	pend := []farm.ApprovalRequest{{ID: "a1", Part: "p1", Tool: "shell", Summary: "ls"}}
	home, _ := ctlHome(t, ctlFarm, pend, func(farm.AttachRequest) *farm.AttachReply {
		return &farm.AttachReply{Error: "already answered (yes, by host)"}
	})
	out, _ := ctlAttach(t, home, strings.NewReader("n\n"))
	waitOutput(t, out, "already answered (yes, by host)")
}

func TestAttach_ControlUndelivered(t *testing.T) {
	home, ff := ctlHome(t, ctlFarm, nil, nil)
	out, _ := ctlAttach(t, home, idleInput(t))
	ff.push(t, farm.AttachNotice{Type: farm.NoticeSteerUndelivered, Part: "p1", Texts: []string{"use slog"}})
	waitOutput(t, out, "[p1] not delivered (part finished): use slog")
}

func TestAttach_ControlApprovalNotices(t *testing.T) {
	home, ff := ctlHome(t, ctlFarm, nil, nil)
	out, _ := ctlAttach(t, home, idleInput(t))
	req := farm.ApprovalRequest{ID: "a3", Part: "backend-api", Tool: "shell", Summary: "npm install zod"}
	ff.push(t, farm.AttachNotice{Type: "approval_requested", Request: &req})
	waitOutput(t, out, "[backend-api] approve [a3] shell: npm install zod (y/n/always/farm)")
	ff.push(t, farm.AttachNotice{Type: "approval_closed", Request: &req, Answer: "yes", By: "host"})
	waitOutput(t, out, "approval [a3] closed: yes (by host)")
}

func TestAttach_ControlFarmEnds(t *testing.T) {
	home, ff := ctlHome(t, ctlFarm, nil, nil)
	out, wait := ctlAttach(t, home, idleInput(t))
	waitOutput(t, out, "p1")
	ff.waitClient(t)
	writeAppend(t, filepath.Join(farmDir(home, ctlFarm), "farm.jsonl"), evLine(farm.EvFarmEnded, "reason", "done"))
	_ = ff.c.Close()
	_ = ff.release()
	if err := wait(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "farm ended: done") {
		t.Fatalf("no end line:\n%s", out.String())
	}
}

func TestAttach_ControlUnknownCommand(t *testing.T) {
	home, ff := ctlHome(t, ctlFarm, nil, nil)
	out, _ := ctlAttach(t, home, strings.NewReader("frobnicate\nsteer p1\n"))
	waitOutput(t, out, "commands: steer <part> <text>")
	waitOutput(t, out, "usage: steer <part> <text>")
	select {
	case r := <-ff.reqs:
		t.Fatalf("sent %+v", r)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestAttach_ControlSanitizes(t *testing.T) {
	home, ff := ctlHome(t, ctlFarm, nil, nil)
	out, _ := ctlAttach(t, home, idleInput(t))
	req := farm.ApprovalRequest{ID: "a1", Part: "p1", Tool: "shell", Summary: "echo \x1b[31mred\x1b[0m"}
	ff.push(t, farm.AttachNotice{Type: "approval_requested", Request: &req})
	waitOutput(t, out, "approve [a1] shell: echo")
	if strings.Contains(out.String(), "\x1b") {
		t.Fatalf("escape reached the terminal: %q", out.String())
	}
}

func TestAttach_ControlSocketClosesLockHeld(t *testing.T) {
	home, ff := ctlHome(t, ctlFarm, nil, nil)
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	out, wait := ctlAttach(t, home, pr)
	ff.waitClient(t)
	_ = ff.c.Close()
	// attach must keep running; a command now reports control unavailable.
	// The close is noticed asynchronously, so retry the command until it is.
	deadline := time.Now().Add(ctlWait)
	for !strings.Contains(out.String(), attachNoControl) {
		if time.Now().After(deadline) {
			t.Fatalf("no unavailable notice:\n%s", out.String())
		}
		_, _ = io.WriteString(pw, "steer p1 x\n")
		time.Sleep(20 * time.Millisecond)
	}
	if strings.Contains(out.String(), "farm ended") {
		t.Fatalf("attach ended early:\n%s", out.String())
	}
	writeAppend(t, filepath.Join(farmDir(home, ctlFarm), "farm.jsonl"), evLine(farm.EvFarmEnded, "reason", "done"))
	_ = ff.release()
	if err := wait(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "farm ended: done") {
		t.Fatalf("no end line:\n%s", out.String())
	}
}

func TestAttach_WatchOnlyWithoutSocket(t *testing.T) {
	home := t.TempDir()
	fixtureFarm(t, home, ctlFarm, []string{"p1"})
	release := lockFixture(t, home, ctlFarm)
	out, wait := ctlAttach(t, home, strings.NewReader("y\n"))
	waitOutput(t, out, attachNoControl)
	if _, err := os.Stat(farm.SockPath(home, ctlFarm)); err == nil {
		t.Fatal("a socket exists; nothing could have been sent")
	}
	writeAppend(t, filepath.Join(farmDir(home, ctlFarm), "farm.jsonl"), evLine(farm.EvFarmEnded, "reason", "done"))
	_ = release()
	if err := wait(); err != nil {
		t.Fatal(err)
	}
	// stdin is not read without control: "y" yields the notice once, no
	// help or "no pending approval" line.
	if got := strings.Count(out.String(), attachNoControl); got != 1 {
		t.Fatalf("notice printed %d times:\n%s", got, out.String())
	}
	if strings.Contains(out.String(), "no pending approval") || !strings.Contains(out.String(), "farm ended: done") {
		t.Fatalf("unexpected output:\n%s", out.String())
	}
}

func TestAttach_NoInputWatchesOnly(t *testing.T) {
	// No terminal on stdin (attach in the background or in a pipe): attach
	// watches, never connects for control, and says nothing about it.
	home, ff := ctlHome(t, ctlFarm, nil, nil)
	out, wait := ctlAttach(t, home, nil)
	waitOutput(t, out, "p1")
	select {
	case <-ff.ready:
		t.Fatal("attach connected for control with no command input")
	case <-time.After(200 * time.Millisecond):
	}
	writeAppend(t, filepath.Join(farmDir(home, ctlFarm), "farm.jsonl"), evLine(farm.EvFarmEnded, "reason", "done"))
	_ = ff.release()
	if err := wait(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), attachNoControl) {
		t.Fatalf("control notice printed:\n%s", out.String())
	}
}

func TestAttach_ControlStopPrintsOK(t *testing.T) {
	home, ff := ctlHome(t, ctlFarm, nil, nil)
	out, _ := ctlAttach(t, home, strings.NewReader("steer p1 x\nstop\n"))
	ff.next(t)
	if r := ff.next(t); r.Cmd != farm.AttachStop {
		t.Fatalf("got %+v", r)
	}
	waitOutput(t, out, "\nok\n")
	time.Sleep(50 * time.Millisecond) // a late reply line would show here
	n := 0
	for _, l := range strings.Split(out.String(), "\n") {
		if l == "ok" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("ok printed %d times (steer must stay silent):\n%s", n, out.String())
	}
}
