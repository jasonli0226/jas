package farm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/jasonli0226/jas/core"
)

// FollowOutcome is how Follow.Run ended.
type FollowOutcome int

const (
	FollowEnded    FollowOutcome = iota + 1 // farm_ended was read
	FollowDied                              // the farm process is gone without farm_ended
	FollowDetached                          // ctx was cancelled
)

const defaultFollowPoll = 250 * time.Millisecond

// Follow reads a farm's logs from disk as they grow and reports each event
// to Watch, the way the supervisor reports them live: farm.jsonl events as
// FarmEvent, every part's events*.jsonl (one per attempt) as WorkerEvent.
// Only complete lines are delivered; bad lines are skipped.
type Follow struct {
	Dir     string // the farm dir
	Watch   Watcher
	Backlog int           // tool_started events replayed per part at start
	Poll    time.Duration // 0 = 250ms
	Running func() bool   // nil = always running
}

// tail is one log file being followed.
type tail struct {
	path string
	part string // "" for farm.jsonl
	off  int64
	rest []byte // an incomplete last line
}

// Run follows the logs until farm_ended is read, Running reports false (after
// one last read), or ctx ends.
func (f *Follow) Run(ctx context.Context) (FollowOutcome, error) {
	poll := f.Poll
	if poll <= 0 {
		poll = defaultFollowPoll
	}
	tails := map[string]*tail{}
	farmLog := &tail{path: filepath.Join(f.Dir, farmLogName), off: fileSize(filepath.Join(f.Dir, farmLogName))}
	tails[farmLog.path] = farmLog
	f.startParts(tails)
	for {
		if f.readAll(tails) {
			return FollowEnded, nil
		}
		if f.Running != nil && !f.Running() {
			if f.readAll(tails) {
				return FollowEnded, nil
			}
			return FollowDied, nil
		}
		select {
		case <-ctx.Done():
			return FollowDetached, nil
		case <-time.After(poll):
		}
	}
}

// startParts adds the part logs present at start, following from the end
// of their last complete line, and replays the last Backlog tool_started
// events of each part.
func (f *Follow) startParts(tails map[string]*tail) {
	backlog := map[string][]map[string]any{}
	var parts []string
	for _, pf := range partLogs(f.Dir) {
		data, _ := os.ReadFile(pf.path)
		done := data[:bytes.LastIndexByte(data, '\n')+1]
		tails[pf.path] = &tail{path: pf.path, part: pf.part, off: int64(len(done))}
		if f.Backlog <= 0 {
			continue
		}
		for _, ln := range bytes.Split(done, []byte{'\n'}) {
			if typ, m, ok := decodeLine(ln); ok && typ == core.EvToolStarted {
				if _, seen := backlog[pf.part]; !seen {
					parts = append(parts, pf.part)
				}
				backlog[pf.part] = append(backlog[pf.part], m)
			}
		}
	}
	for _, part := range parts {
		evs := backlog[part]
		for _, m := range evs[max(0, len(evs)-f.Backlog):] {
			f.Watch.WorkerEvent(part, core.EvToolStarted, m)
		}
	}
}

// readAll picks up new part logs and reads every tail; true once farm_ended
// was delivered.
func (f *Follow) readAll(tails map[string]*tail) bool {
	for _, pf := range partLogs(f.Dir) {
		if _, ok := tails[pf.path]; !ok {
			tails[pf.path] = &tail{path: pf.path, part: pf.part}
		}
	}
	paths := make([]string, 0, len(tails))
	for p := range tails {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	ended := false
	for _, p := range paths {
		if f.read(tails[p]) {
			ended = true
		}
	}
	return ended
}

// read delivers the complete lines appended to t since the last read.
func (f *Follow) read(t *tail) (ended bool) {
	fh, err := os.Open(t.path)
	if err != nil {
		return false
	}
	defer fh.Close()
	if _, err := fh.Seek(t.off, io.SeekStart); err != nil {
		return false
	}
	data, err := io.ReadAll(fh)
	if err != nil || len(data) == 0 {
		return false
	}
	t.off += int64(len(data))
	buf := append(t.rest, data...)
	i := bytes.LastIndexByte(buf, '\n')
	if i < 0 {
		t.rest = buf
		return false
	}
	t.rest = append([]byte(nil), buf[i+1:]...)
	for _, ln := range bytes.Split(buf[:i], []byte{'\n'}) {
		typ, m, ok := decodeLine(ln)
		if !ok {
			continue
		}
		if t.part == "" {
			f.Watch.FarmEvent(typ, m)
			ended = ended || typ == EvFarmEnded
		} else {
			f.Watch.WorkerEvent(t.part, typ, m)
		}
	}
	return ended
}

// decodeLine decodes one log line; args stay raw JSON, as the supervisor
// hands them to watchers.
func decodeLine(ln []byte) (string, map[string]any, bool) {
	if len(bytes.TrimSpace(ln)) == 0 {
		return "", nil, false
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(ln, &raw) != nil {
		return "", nil, false
	}
	var typ string
	if json.Unmarshal(raw["type"], &typ) != nil || typ == "" {
		return "", nil, false
	}
	m := make(map[string]any, len(raw))
	for k, v := range raw {
		if k == "args" {
			m[k] = v
			continue
		}
		var x any
		if json.Unmarshal(v, &x) == nil {
			m[k] = x
		}
	}
	return typ, m, true
}

type partLog struct{ part, path string }

// partLogs lists parts/<part>/events*.jsonl, sorted by path.
func partLogs(dir string) []partLog {
	matches, _ := filepath.Glob(filepath.Join(dir, partsDir, "*", "events*.jsonl"))
	sort.Strings(matches)
	out := make([]partLog, 0, len(matches))
	for _, p := range matches {
		out = append(out, partLog{part: filepath.Base(filepath.Dir(p)), path: p})
	}
	return out
}

func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}
