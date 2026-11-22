package farm

// The attach socket's wire format: JSON lines over <home>/run/<id>.sock.
// A client sends one AttachRequest per line; the server answers each with
// an AttachMessage carrying a Reply (same id), and pushes AttachMessages
// carrying a Notice (approval_requested, approval_closed, steer_undelivered).

// AttachProtocol is the socket protocol version hello reports.
const AttachProtocol = 1

// Attach commands (AttachRequest.Cmd).
const (
	AttachHello   = "hello"
	AttachSteer   = "steer"
	AttachPause   = "pause"
	AttachResume  = "resume"
	AttachDrop    = "drop"
	AttachStop    = "stop"
	AttachApprove = "approve"
)

// NoticeApprovalGranted is the notice type that says what an always or
// farm answer granted (Text, as the farm's own terminal prints it).
const NoticeApprovalGranted = "approval_granted"

// NoticeSteerUndelivered is the notice type that hands a client back the
// steer texts it sent to a part that finished before reading them.
const NoticeSteerUndelivered = "steer_undelivered"

// AttachRequest is one client command.
type AttachRequest struct {
	ID         string `json:"id"`
	Cmd        string `json:"cmd"` // hello|steer|pause|resume|drop|stop|approve
	Part       string `json:"part,omitempty"`
	Text       string `json:"text,omitempty"`
	Hard       bool   `json:"hard,omitempty"`
	ApprovalID string `json:"approval_id,omitempty"`
	Answer     string `json:"answer,omitempty"`
}

// AttachMessage is one line from the server: a reply or a notice.
type AttachMessage struct {
	Reply  *AttachReply  `json:"reply,omitempty"`
	Notice *AttachNotice `json:"notice,omitempty"`
}

// AttachReply answers the request with the same ID.
type AttachReply struct {
	ID       string            `json:"id"`
	OK       bool              `json:"ok"`
	Error    string            `json:"error,omitempty"`
	FarmID   string            `json:"farm_id,omitempty"`
	Protocol int               `json:"protocol,omitempty"`
	Client   string            `json:"client,omitempty"`  // hello only: this client as approvals' "by" names it
	Pending  []ApprovalRequest `json:"pending,omitempty"` // hello only
}

// AttachNotice is pushed to clients that said hello (steer_undelivered: to
// the steer's sender only).
type AttachNotice struct {
	Type    string           `json:"type"` // approval_requested|approval_closed|approval_granted|steer_undelivered
	Request *ApprovalRequest `json:"request,omitempty"`
	Answer  string           `json:"answer,omitempty"`
	By      string           `json:"by,omitempty"`
	Part    string           `json:"part,omitempty"`
	Text    string           `json:"text,omitempty"` // approval_granted
	Texts   []string         `json:"texts,omitempty"`
}

// SockPath is the running farm's attach socket.
func SockPath(home, id string) string { return sockPath(home, id) }
