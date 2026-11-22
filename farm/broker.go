package farm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jasonli0226/jas/core"
)

// Part-log event types the broker writes around every gated call it asks
// about. core logs the bare decision as "approval" beside them.
const (
	EvApprovalRequested = "approval_requested" // before anyone is asked
	EvApprovalClosed    = "approval_closed"    // when the first answer (or a refusal) closes it
)

// defaultApprovalWindow is how long an unattended farm holds a gated call
// when no approval wait is set.
const defaultApprovalWindow = 10 * time.Minute

// noticeBuffer is each subscriber's channel size; a full channel drops.
const noticeBuffer = 64

// Who closed an approval that nobody answered, and the answer it gets.
const (
	answerRefused   = "refused"
	answerCancelled = "cancelled" // the part's ctx ended first
	byHost          = "host"
	byGrant         = "grant"     // a standing grant covered it while it waited
	byTimeout       = "timeout"   // the hold window ended
	byNoClient      = "no_client" // nobody to ask, or the last client left
	byDeadline      = "deadline"  // the part's ctx deadline passed
	byCancel        = "cancel"    // the part's ctx was cancelled
	byStop          = "stop"      // the farm soft-stopped while it was held
)

// Answer is an answer to an approval: the chat's words.
type Answer int

const (
	AnswerNo     Answer = iota // deny
	AnswerYes                  // allow once
	AnswerAlways               // allow; a standing grant for this part
	AnswerFarm                 // allow; a standing grant for every part
)

// ParseAnswer reads y/yes, n/no/"", always and farm, ignoring case and
// surrounding space. Anything else is unknown (no, ok false): no single
// letter makes a standing grant.
func ParseAnswer(s string) (Answer, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "n", "no":
		return AnswerNo, true
	case "y", "yes":
		return AnswerYes, true
	case "always":
		return AnswerAlways, true
	case "farm":
		return AnswerFarm, true
	}
	return AnswerNo, false
}

// String returns "yes", "no", "always" or "farm".
func (a Answer) String() string {
	switch a {
	case AnswerYes:
		return "yes"
	case AnswerAlways:
		return "always"
	case AnswerFarm:
		return "farm"
	}
	return "no"
}

// ApprovalRequest is one pending gated call.
type ApprovalRequest struct {
	ID      string     `json:"id"`
	Part    string     `json:"part"`
	Tool    string     `json:"tool"`
	Summary string     `json:"summary"` // core.SummarizeCall of the redacted args
	Diff    *core.Diff `json:"-"`       // display only; nil when none
}

// HostAsker asks the farm's own terminal or chat; it must stop asking when
// ctx is done. nil = no host (unattended). A host that shows one prompt at a
// time should keep its prompt slot until ctx ends even after answering: the
// broker records the answer's grant first (releasing identical calls that
// wait), and a request that closed while the host waited for the slot
// (Broker.Closed) should not be shown.
type HostAsker func(ctx context.Context, req ApprovalRequest) (Answer, error)

// Notice tells subscribers an approval opened or closed.
type Notice struct {
	Type    string // EvApprovalRequested | EvApprovalClosed
	Request ApprovalRequest
	Answer  string // closed: answer or "refused"
	By      string // closed: "host" | "attach:<client>" | "grant" | "timeout" | "no_client" | "stop" | "deadline" | "cancel"
}

// Notices for an always or farm answer that grants nothing standing; the
// chat printed the same texts before farms had a broker.
const (
	NoGrantNotice = "this command can't get an always grant; allowed once"
	MustAskNotice = "writes to the profile's skills always ask; allowed once"
)

// GrantKind is what an always or farm answer did.
type GrantKind int

const (
	GrantAdded   GrantKind = iota // a standing grant (Grant names it)
	GrantNone                     // the call cannot be granted; allowed once
	GrantMustAsk                  // a must-ask call; allowed once
)

// GrantNotice reports an always or farm answer's grant to the host.
type GrantNotice struct {
	Part  string
	Kind  GrantKind
	Grant string // GrantAdded: the grant as core.Grants.Add shows it
}

// Text is the line a host prints for n (unredacted).
func (n GrantNotice) Text() string {
	switch n.Kind {
	case GrantNone:
		return NoGrantNotice
	case GrantMustAsk:
		return MustAskNotice
	}
	return fmt.Sprintf("[%s] always: %s (covers any arguments)", n.Part, core.Sanitize(n.Grant))
}

// BrokerOptions configures a Broker.
type BrokerOptions struct {
	Level   core.Level    // used when a role sets no approve level
	Host    HostAsker     // nil: unattended
	Wait    time.Duration // budget.approval_wait / --approval-wait; 0 = hold only while attached
	Windows bool          // core.NewGrants
	Redact  func(string) string
	After   func(time.Duration) <-chan time.Time // nil = time.After (tests inject)
	// OnGrant hears what each always or farm answer granted, before the
	// host's ctx for that call ends; nil: nobody.
	OnGrant func(GrantNotice)
}

// Broker is every farm part's approval gate. Each gated call becomes a
// pending approval with a farm-unique id; the host asker (when there is
// one) and attached clients may answer it, and the first answer wins.
type Broker struct {
	o    BrokerOptions
	farm *core.Grants // "farm" grants, shared by every part

	mu      sync.Mutex
	seq     int
	clients int
	stopped bool // Stop: held approvals are refused
	pending map[string]*approval
	closed  map[string]outcome // answered ids, for late answers
	subs    map[int]chan Notice
	subSeq  int
}

// outcome is how an approval closed.
type outcome struct {
	answer  Answer
	refused bool   // closed with no answer
	auto    bool   // closed by a standing grant: AutoGranted
	text    string // the answer as logged: Answer.String(), "refused" or "cancelled"
	by      string
}

type approval struct {
	seq  int
	req  ApprovalRequest
	g    *brokerGate // the asking part's gate, for coverage
	t    core.Tool
	args json.RawMessage
	held bool          // unattended: refused when the last client leaves (Wait 0)
	done chan struct{} // closed with out set
	out  outcome
}

// NewBroker returns a broker with no clients.
func NewBroker(o BrokerOptions) *Broker {
	if o.Redact == nil {
		o.Redact = func(s string) string { return s }
	}
	if o.After == nil {
		o.After = time.After
	}
	return &Broker{
		o: o, farm: core.NewGrants(o.Windows),
		pending: map[string]*approval{}, closed: map[string]outcome{}, subs: map[int]chan Notice{},
	}
}

// Gate returns the gate for one part at the role's approve level ("" means
// the default level). Its approval events go to log (nil: none).
func (b *Broker) Gate(part, approve string, log core.EventLog) (core.Gate, error) {
	level := b.o.Level
	if approve != "" {
		l, err := core.ParseLevel(approve)
		if err != nil {
			return nil, err
		}
		level = l
	}
	return &brokerGate{b: b, part: part, level: level, log: log, grants: core.NewGrants(b.o.Windows)}, nil
}

// Answer closes approval id with a, by the given source. A closed id gets
// "already answered (<answer>, by <source>)", an unknown one "unknown
// approval"; neither changes anything.
func (b *Broker) Answer(id string, a Answer, by string) (ok bool, msg string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, open := b.pending[id]; open {
		b.closeLocked(id, outcome{answer: a, text: a.String(), by: by})
		return true, ""
	}
	if out, done := b.closed[id]; done {
		return false, fmt.Sprintf("already answered (%s, by %s)", out.text, out.by)
	}
	return false, "unknown approval"
}

// Closed reports how approval id closed: the answer as logged and who gave
// it. ok is false while id is open or unknown. A close is recorded before a
// withdrawn host's ctx is cancelled, so the host can read it at once.
func (b *Broker) Closed(id string) (answer, by string, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	out, ok := b.closed[id]
	return out.text, out.by, ok
}

// Pending returns the open approvals, oldest first.
func (b *Broker) Pending() []ApprovalRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	as := make([]*approval, 0, len(b.pending))
	for _, a := range b.pending {
		as = append(as, a)
	}
	sort.Slice(as, func(i, j int) bool { return as[i].seq < as[j].seq })
	reqs := make([]ApprovalRequest, len(as))
	for i, a := range as {
		reqs[i] = a.req
	}
	return reqs
}

// Subscribe returns a channel of notices and a func that ends the
// subscription (and closes the channel; safe to call more than once). The
// channel is buffered: a subscriber that falls behind misses notices, and
// never blocks a gate.
func (b *Broker) Subscribe() (<-chan Notice, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subSeq++
	id, ch := b.subSeq, make(chan Notice, noticeBuffer)
	b.subs[id] = ch
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			delete(b.subs, id)
			close(ch)
		})
	}
}

// SetClients records how many attached clients are present. With no
// approval wait, the last client leaving refuses every held approval.
func (b *Broker) SetClients(n int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.clients = n
	if n > 0 || b.o.Wait > 0 {
		return
	}
	for id, a := range b.pending {
		if a.held {
			b.closeLocked(id, outcome{refused: true, text: answerRefused, by: byNoClient})
		}
	}
}

// notifyLocked sends n to every subscriber that has room.
func (b *Broker) notifyLocked(n Notice) {
	for _, ch := range b.subs {
		select {
		case ch <- n:
		default:
		}
	}
}

// closeLocked closes an open approval with out and tells subscribers.
func (b *Broker) closeLocked(id string, out outcome) {
	a := b.pending[id]
	delete(b.pending, id)
	b.closed[id] = out
	a.out = out
	close(a.done)
	b.notifyLocked(Notice{Type: EvApprovalClosed, Request: a.req, Answer: out.text, By: out.by})
}

// close closes id with out unless an answer already closed it.
func (b *Broker) close(id string, out outcome) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, open := b.pending[id]; open {
		b.closeLocked(id, out)
	}
}

// open registers a request logged as requested. A grant recorded since the
// gate's coverage check closes it at once as granted; unattended with no
// client and no wait, it is refused at once (still logged and announced
// first). After Stop, a held request is refused at once.
func (b *Broker) open(g *brokerGate, t core.Tool, args json.RawMessage, req ApprovalRequest, seq int) *approval {
	b.mu.Lock()
	defer b.mu.Unlock()
	a := &approval{seq: seq, req: req, g: g, t: t, args: args, held: b.o.Host == nil, done: make(chan struct{})}
	b.pending[req.ID] = a
	b.notifyLocked(Notice{Type: EvApprovalRequested, Request: req})
	if g.covered(t, args) {
		b.closeLocked(req.ID, grantedOutcome)
		return a
	}
	switch {
	case a.held && b.stopped:
		b.closeLocked(req.ID, outcome{refused: true, text: answerRefused, by: byStop})
	case a.held && b.clients == 0 && b.o.Wait == 0:
		b.closeLocked(req.ID, outcome{refused: true, text: answerRefused, by: byNoClient})
	}
	return a
}

// grantedOutcome closes a call a standing grant now covers.
var grantedOutcome = outcome{answer: AnswerYes, auto: true, text: AnswerYes.String(), by: byGrant}

// releaseCovered closes every open approval a standing grant now covers, so
// identical calls waiting behind the answered one are not asked again.
func (b *Broker) releaseCovered() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, a := range b.pending {
		if a.g.covered(a.t, a.args) {
			b.closeLocked(id, grantedOutcome)
		}
	}
}

// nextID returns a new farm-unique approval id.
func (b *Broker) nextID() (string, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.seq++
	return fmt.Sprintf("a%d", b.seq), b.seq
}

// window is how long an unattended approval is held.
func (b *Broker) window() time.Duration {
	if b.o.Wait > 0 {
		return b.o.Wait
	}
	return defaultApprovalWindow
}

// brokerGate is one part's core.Gate.
type brokerGate struct {
	b      *Broker
	part   string
	level  core.Level
	log    core.EventLog
	grants *core.Grants // "always" for this part
}

var _ core.Gate = (*brokerGate)(nil)

func (g *brokerGate) covered(t core.Tool, args json.RawMessage) bool {
	if !core.Gated(g.level, t) {
		return true
	}
	return !core.MustAsk(t) && (g.grants.Covers(t.Name(), args) || g.b.farm.Covers(t.Name(), args))
}

func (g *brokerGate) append(typ string, data map[string]any) error {
	if g.log == nil {
		return nil
	}
	if err := g.log.Append(typ, data); err != nil {
		return fmt.Errorf("log %s: %w", typ, err)
	}
	return nil
}

// Allow implements core.Gate. An approval event that cannot be logged
// fails the call closed (Denied with the error).
func (g *brokerGate) Allow(ctx context.Context, t core.Tool, args json.RawMessage, preview func() *core.Diff) (core.Decision, error) {
	if g.covered(t, args) {
		return core.AutoGranted, nil
	}
	b := g.b
	id, seq := b.nextID()
	req := ApprovalRequest{
		ID: id, Part: g.part, Tool: t.Name(),
		Summary: core.SummarizeCall(t.Name(), json.RawMessage(b.o.Redact(string(args)))),
	}
	if preview != nil {
		req.Diff = preview()
	}
	if err := g.append(EvApprovalRequested, map[string]any{
		"approval_id": req.ID, "tool": req.Tool, "summary": req.Summary,
	}); err != nil {
		return core.Denied, err
	}
	a := b.open(g, t, args, req, seq)
	// The host's ctx ends once the call is decided (withdrawing its prompt
	// if something else answered), after any grant is recorded.
	hctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out, err := g.wait(ctx, hctx, a)
	if lerr := g.append(EvApprovalClosed, map[string]any{
		"approval_id": req.ID, "answer": out.text, "by": out.by,
	}); lerr != nil && err == nil {
		return core.Denied, lerr
	}
	if err != nil {
		return core.Denied, err
	}
	return g.decide(t, args, out), nil
}

// wait blocks until a closes, asking the host (on hctx) when there is one.
// ctx done closes a as cancelled and returns ctx.Err(), unless an answer
// won first.
func (g *brokerGate) wait(ctx, hctx context.Context, a *approval) (outcome, error) {
	b := g.b
	select {
	case <-a.done: // granted or refused at once
		return a.out, nil
	default:
	}
	var window <-chan time.Time
	if b.o.Host != nil {
		go g.askHost(hctx, a.req)
	} else {
		window = b.o.After(b.window())
	}
	select {
	case <-a.done:
	case <-window:
		b.close(a.req.ID, outcome{refused: true, text: answerRefused, by: byTimeout})
	case <-ctx.Done():
		by := byCancel
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			by = byDeadline
		}
		b.close(a.req.ID, outcome{refused: true, text: answerCancelled, by: by})
		<-a.done
		if a.out.text == answerCancelled {
			return a.out, ctx.Err()
		}
		return a.out, nil
	}
	<-a.done
	return a.out, nil
}

// askHost puts req to the host asker and applies its answer, unless
// something else closed req first (then hctx is cancelled). A host error
// that is not that withdrawal is a refusal by the host, as an EOF on the
// TTY prompt is today.
func (g *brokerGate) askHost(hctx context.Context, req ApprovalRequest) {
	ans, err := g.b.o.Host(hctx, req)
	if err != nil {
		if hctx.Err() != nil {
			return
		}
		ans = AnswerNo
	}
	g.b.close(req.ID, outcome{answer: ans, text: ans.String(), by: byHost})
}

// decide turns a closed approval into a decision, recording a standing
// grant for always/farm (never for a must-ask call), telling OnGrant, and
// releasing the open calls the grant covers.
func (g *brokerGate) decide(t core.Tool, args json.RawMessage, out outcome) core.Decision {
	if out.refused {
		return core.Unavailable
	}
	if out.auto {
		return core.AutoGranted
	}
	var grants *core.Grants
	switch out.answer {
	case AnswerYes:
		return core.Granted
	case AnswerAlways:
		grants = g.grants
	case AnswerFarm:
		grants = g.b.farm
	default:
		return core.Denied
	}
	n := GrantNotice{Part: g.part, Kind: GrantMustAsk}
	if !core.MustAsk(t) {
		grant, ok := grants.Add(t.Name(), args) // an ungrantable call is allowed once
		n.Kind, n.Grant = GrantNone, grant
		if ok {
			n.Kind = GrantAdded
		}
	}
	if g.b.o.OnGrant != nil {
		g.b.o.OnGrant(n)
	}
	if n.Kind == GrantAdded {
		g.b.releaseCovered()
	}
	return core.Granted
}

// Stop is the farm's soft stop: every held approval (no host) closes as
// refused by "stop", and later held calls are refused at once, so a held
// call never keeps a stopping farm alive. A host keeps prompting.
func (b *Broker) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.stopped = true
	for id, a := range b.pending {
		if a.held {
			b.closeLocked(id, outcome{refused: true, text: answerRefused, by: byStop})
		}
	}
}
