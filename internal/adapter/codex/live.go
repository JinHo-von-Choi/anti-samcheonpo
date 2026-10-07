package codex

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/claude"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// Tracker follows a growing rollout file during a live session: token usage
// deltas (from token_count totals) and command exit codes (from
// item_completed CommandExecution, keyed by the same id the hooks see as
// tool_use_id).
type Tracker struct {
	Path   string
	Offset int64
	Model  string
	Quota  *cost.Quota
	last   tokenUsage
	Exits  map[string]int
	buf    []byte
}

// NewTracker creates a tracker that skips usage already in the file (earlier
// runs of a resumed session) but remembers the running totals and model.
func NewTracker(path string) *Tracker {
	t := &Tracker{Path: path, Exits: map[string]int{}}
	_, _ = t.Poll()
	return t
}

// Poll reads new complete lines and returns usage deltas in order.
func (t *Tracker) Poll() ([]event.Usage, error) {
	f, err := os.Open(t.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(t.Offset, io.SeekStart); err != nil {
		return nil, err
	}
	rd := bufio.NewReaderSize(f, 1<<20)
	var out []event.Usage
	for {
		line, err := claude.ReadLine(rd, &t.buf)
		if err != nil {
			break // a partial last line is read again next time
		}
		t.Offset += int64(len(line))
		if u, ok := t.feed(line); ok {
			out = append(out, u)
		}
	}
	return out, nil
}

var (
	bTokenCount = []byte(`"token_count"`)
	bTurnCtx    = []byte(`"turn_context"`)
)

func (t *Tracker) feed(line []byte) (event.Usage, bool) {
	switch {
	case bytes.Contains(line, bTokenCount):
		var r struct {
			Payload struct {
				RateLimits json.RawMessage `json:"rate_limits"`
				Info       *struct {
					Total tokenUsage `json:"total_token_usage"`
				} `json:"info"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &r) != nil {
			return event.Usage{}, false
		}
		if len(r.Payload.RateLimits) > 0 {
			t.Quota = parseQuota(r.Payload.RateLimits)
		}
		if r.Payload.Info == nil {
			return event.Usage{}, false
		}
		tot := r.Payload.Info.Total
		if tot.Total <= t.last.Total {
			return event.Usage{}, false
		}
		d := tokenUsage{In: tot.In - t.last.In, Cached: tot.Cached - t.last.Cached, CacheW: tot.CacheW - t.last.CacheW, Out: tot.Out - t.last.Out}
		t.last = tot
		return event.Usage{In: max(0, d.In-d.Cached), CacheRead: d.Cached, CacheWrite: d.CacheW, Out: d.Out, Model: t.Model}, true
	case bytes.Contains(line, bItemCompleted) && bytes.Contains(line, bCmdExec):
		var r struct {
			Payload struct {
				Item struct {
					ID       string   `json:"id"`
					ExitCode *float64 `json:"exit_code"`
				} `json:"item"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &r) == nil && r.Payload.Item.ID != "" && r.Payload.Item.ExitCode != nil {
			t.Exits[r.Payload.Item.ID] = int(*r.Payload.Item.ExitCode)
		}
	case bytes.Contains(line, bTurnCtx):
		var r struct {
			Payload struct {
				Model string `json:"model"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &r) == nil && r.Payload.Model != "" {
			t.Model = r.Payload.Model
		}
	}
	return event.Usage{}, false
}
