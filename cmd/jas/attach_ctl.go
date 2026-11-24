package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jasonli0226/jas/core"
	"github.com/jasonli0226/jas/farm"
)

// jas attach (#156, phase 2): the command line and the approval inbox, over
// the farm's attach socket (farm.SockPath). Without a socket attach watches
// only.

const (
	attachDialWait  = 2 * time.Second
	attachHelloWait = 5 * time.Second
	attachMaxLine   = 1 << 20

	attachNoControl = "control unavailable (watching only)"
	attachHelp      = "commands: steer <part> <text> | pause <part> | resume <part> | drop <part> | stop [hard] | status | y|n|a|always|farm [<approval-id>]"
)

// attachCtl is one attach client's control connection.
type attachCtl struct {
	conn   net.Conn
	out    io.Writer
	redact func(string) string
	status func() // re-prints the farm status table

	self  string     // this client as the farm's "by" names it (from hello)
	wmu   sync.Mutex // serializes writes and the request counter
	seq   int
	mu    sync.Mutex
	open  bool                   // the socket is still up
	pend  []farm.ApprovalRequest // oldest first
	stops map[string]bool        // ids of stop requests awaiting a reply
}

// dialAttachCtl connects to the farm's socket and says hello. It prints
// attachNoControl and returns nil when there is no usable socket.
func dialAttachCtl(home, id string, out io.Writer, redact func(string) string, status func()) *attachCtl {
	conn, err := net.DialTimeout("unix", farm.SockPath(home, id), attachDialWait)
	if err != nil {
		fmt.Fprintln(out, attachNoControl)
		return nil
	}
	c := &attachCtl{conn: conn, out: out, redact: redact, status: status, open: true}
	rd := bufio.NewReaderSize(conn, 64*1024)
	_ = conn.SetDeadline(time.Now().Add(attachHelloWait))
	rep, err := c.hello(rd)
	_ = conn.SetDeadline(time.Time{})
	if err != nil || !rep.OK || rep.Protocol != farm.AttachProtocol {
		_ = conn.Close()
		fmt.Fprintln(out, attachNoControl)
		return nil
	}
	c.self = rep.Client
	for _, p := range rep.Pending {
		c.addPending(p)
	}
	c.printPending() // before the reader, so a notice is not printed twice
	go c.readLoop(rd)
	return c
}

func (c *attachCtl) hello(rd *bufio.Reader) (farm.AttachReply, error) {
	if err := c.send(farm.AttachRequest{Cmd: farm.AttachHello}); err != nil {
		return farm.AttachReply{}, err
	}
	for {
		line, err := rd.ReadBytes('\n')
		if err != nil {
			return farm.AttachReply{}, err
		}
		var m farm.AttachMessage
		if json.Unmarshal(line, &m) == nil && m.Reply != nil {
			return *m.Reply, nil
		}
	}
}

// send writes one request line, giving it an id.
func (c *attachCtl) send(req farm.AttachRequest) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.seq++
	req.ID = fmt.Sprintf("r%d", c.seq)
	if req.Cmd == farm.AttachStop {
		c.mu.Lock()
		if c.stops == nil {
			c.stops = map[string]bool{}
		}
		c.stops[req.ID] = true
		c.mu.Unlock()
	}
	b, err := json.Marshal(req)
	if err != nil {
		return err
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(attachHelloWait))
	_, err = c.conn.Write(append(b, '\n'))
	return err
}

func (c *attachCtl) print(s string) {
	fmt.Fprintln(c.out, core.Sanitize(c.redact(s)))
}

func (c *attachCtl) close() { _ = c.conn.Close() }

// readLoop prints what the farm pushes until the socket closes; from then
// on control is unavailable and attach keeps watching.
func (c *attachCtl) readLoop(rd *bufio.Reader) {
	defer func() {
		c.mu.Lock()
		c.open = false
		c.mu.Unlock()
		_ = c.conn.Close()
	}()
	for {
		line, err := rd.ReadBytes('\n')
		if err != nil {
			return
		}
		var m farm.AttachMessage
		if json.Unmarshal(line, &m) != nil {
			continue
		}
		if m.Reply != nil {
			c.reply(m.Reply)
		}
		if m.Notice != nil {
			c.notice(m.Notice)
		}
	}
}

// reply prints a failed reply's error, and "ok" for a stop that worked;
// other successful replies print nothing.
func (c *attachCtl) reply(r *farm.AttachReply) {
	c.mu.Lock()
	stop := c.stops[r.ID]
	delete(c.stops, r.ID)
	c.mu.Unlock()
	switch {
	case !r.OK && r.Error != "":
		if c.self != "" {
			r.Error = strings.ReplaceAll(r.Error, "by "+c.self+")", "by you)")
		}
		c.print(r.Error)
	case r.OK && stop:
		c.print("ok")
	}
}

func (c *attachCtl) notice(n *farm.AttachNotice) {
	switch n.Type {
	case "approval_requested":
		if n.Request != nil && c.addPending(*n.Request) {
			c.print(approvalLine(*n.Request))
		}
	case "approval_closed":
		if n.Request == nil {
			return
		}
		c.dropPending(n.Request.ID)
		c.print(fmt.Sprintf("[%s] approval [%s] closed: %s (by %s)", n.Request.Part, n.Request.ID, n.Answer, c.by(n.By)))
	case farm.NoticeApprovalGranted:
		c.print(n.Text)
	case farm.NoticeSteerUndelivered:
		for _, t := range n.Texts {
			c.print(fmt.Sprintf("[%s] not delivered (part finished): %s", n.Part, t))
		}
	}
}

// by names who answered an approval, "you" when it was this client.
func (c *attachCtl) by(who string) string {
	if c.self != "" && who == c.self {
		return "you"
	}
	return who
}

func approvalLine(r farm.ApprovalRequest) string {
	return fmt.Sprintf("[%s] approve [%s] %s: %s (y/n/a/always/farm)", r.Part, r.ID, r.Tool, r.Summary)
}

// addPending records r and prints it for the hello list; false when known.
func (c *attachCtl) addPending(r farm.ApprovalRequest) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, p := range c.pend {
		if p.ID == r.ID {
			return false
		}
	}
	c.pend = append(c.pend, r)
	return true
}

func (c *attachCtl) dropPending(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, p := range c.pend {
		if p.ID == id {
			c.pend = append(c.pend[:i], c.pend[i+1:]...)
			return
		}
	}
}

// printPending prints the approvals waiting at hello.
func (c *attachCtl) printPending() {
	c.mu.Lock()
	list := append([]farm.ApprovalRequest(nil), c.pend...)
	c.mu.Unlock()
	for _, r := range list {
		c.print(approvalLine(r))
	}
}

// commands reads the command line from in until it ends.
func (c *attachCtl) commands(in io.Reader) {
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64*1024), attachMaxLine)
	for sc.Scan() {
		c.command(sc.Text())
	}
}

func (c *attachCtl) command(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	req, msg := c.parse(line)
	if msg != "" {
		c.print(msg)
		return
	}
	if req == nil { // status
		c.status()
		return
	}
	c.mu.Lock()
	open := c.open
	c.mu.Unlock()
	if !open {
		c.print(attachNoControl)
		return
	}
	if err := c.send(*req); err != nil {
		c.print("send failed: " + err.Error())
	}
}

// parse turns a command line into a request; a message means nothing is
// sent, and a nil request with no message is status.
func (c *attachCtl) parse(line string) (*farm.AttachRequest, string) {
	word, rest, _ := strings.Cut(line, " ")
	rest = strings.TrimSpace(rest)
	switch strings.ToLower(word) {
	case "steer":
		part, text, _ := strings.Cut(rest, " ")
		if text = strings.TrimSpace(text); part == "" || text == "" {
			return nil, "usage: steer <part> <text>"
		}
		return &farm.AttachRequest{Cmd: farm.AttachSteer, Part: part, Text: text}, ""
	case "pause", "resume", "drop":
		if rest == "" || strings.ContainsAny(rest, " \t") {
			return nil, "usage: " + word + " <part>"
		}
		return &farm.AttachRequest{Cmd: word, Part: rest}, ""
	case "stop":
		switch rest {
		case "":
			return &farm.AttachRequest{Cmd: farm.AttachStop}, ""
		case "hard":
			return &farm.AttachRequest{Cmd: farm.AttachStop, Hard: true}, ""
		}
		return nil, "usage: stop [hard]"
	case "status":
		return nil, ""
	case "y", "yes", "n", "no", "a", "always", "farm":
		return c.approve(strings.ToLower(word), rest)
	}
	return nil, attachHelp
}

func (c *attachCtl) approve(answer, id string) (*farm.AttachRequest, string) {
	if strings.ContainsAny(id, " \t") {
		return nil, "usage: " + answer + " [<approval-id>]"
	}
	if id == "" {
		c.mu.Lock()
		if len(c.pend) > 0 {
			id = c.pend[0].ID
		}
		c.mu.Unlock()
		if id == "" {
			return nil, "no pending approval"
		}
	}
	return &farm.AttachRequest{Cmd: farm.AttachApprove, ApprovalID: id, Answer: answer}, ""
}

// statusPrinter re-prints the status table from the manifest on disk.
func statusPrinter(out io.Writer, dir string) func() {
	return func() {
		m, err := farm.LoadManifest(filepath.Join(dir, farmManifestFile))
		if err != nil {
			fmt.Fprintln(out, "farm is starting")
			return
		}
		printFarmStatus(out, m)
	}
}
