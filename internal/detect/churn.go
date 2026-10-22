package detect

import (
	"sort"
	"strings"
	"sync"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// churnWindow is how many recent proposals per file are remembered.
const churnWindow = 10

// churnProposal is one content a write proposed for a file.
type churnProposal struct {
	seq  int64
	hash string
}

// ChurnDetector remembers the content each recent write proposed for a
// file.
//
// The detector does not judge a repetition on its own. Writing the same
// content twice in a row is harmless; writing it again while the file sits
// at a state that already failed is the A-B-A loop, where the agent keeps
// re-applying a hypothesis that was already refuted. The caller decides
// that part, using the failed states of the semantic oscillation detector.
type ChurnDetector struct {
	mu    sync.Mutex
	props map[string][]churnProposal
}

// NewChurnDetector creates an empty detector.
func NewChurnDetector() *ChurnDetector {
	return &ChurnDetector{props: map[string][]churnProposal{}}
}

// Record remembers the content a write proposed for path.
func (d *ChurnDetector) Record(path, hash string, seq int64) {
	if path == "" || hash == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	buf := append(d.props[path], churnProposal{seq: seq, hash: hash})
	if len(buf) > churnWindow {
		buf = buf[len(buf)-churnWindow:]
	}
	d.props[path] = buf
}

// Repeat reports whether the same content was proposed for path earlier,
// and when it was proposed first.
func (d *ChurnDetector) Repeat(path, hash string) (seq int64, count int, ok bool) {
	if path == "" || hash == "" {
		return 0, 0, false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	buf := d.props[path]
	for i := len(buf) - 1; i >= 0; i-- {
		if buf[i].hash == hash {
			return buf[i].seq, i + 1, true
		}
	}
	return 0, 0, false
}

// Clear drops the remembered proposals. A passing verification ends the
// sequence, and so does the next attempt.
func (d *ChurnDetector) Clear() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.props = map[string][]churnProposal{}
}

// churnRepeat reports a write that proposes a content this session already
// wrote while the file sits at a state that already failed.
//
// The check is exact: the proposed content hash must match an earlier
// proposal. A rewrite that differs only in formatting is not caught here;
// the semantic oscillation detector judges that shape after the fact.
func (e *Engine) churnRepeat(ev *event.Event) *Signal {
	st := e.St
	paths := make([]string, 0, len(ev.WriteHashes))
	for p := range ev.WriteHashes {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		h := ev.WriteHashes[p]
		// "edit:" marks an edit whose prior state the host did not report, so
		// the proposed content is unknown.
		if h == "" || strings.HasPrefix(h, "edit:") {
			continue
		}
		if !st.semOsc.HasFailed(p) {
			continue
		}
		prev, count, ok := st.churn.Repeat(p, h)
		if !ok {
			continue
		}
		return &Signal{Detector: "S2", Rule: "s2.semantic_oscillation", Confidence: 0.85, Level: L1,
			Facts: map[string]any{"path": p, "prev_seq": prev, "count": count, "blocked": true}}
	}
	return nil
}

// recordChurnProposal remembers what a write proposed, so a later write of
// the same content can be recognized.
func (e *Engine) recordChurnProposal(ev *event.Event) {
	if ev.Category != event.CatProduce {
		return
	}
	if ev.Tool != event.ToolWrite && ev.Tool != event.ToolEdit {
		return
	}
	st := e.St
	for p, h := range ev.WriteHashes {
		if h == "" || strings.HasPrefix(h, "edit:") {
			continue
		}
		st.churn.Record(p, h, ev.Seq)
	}
}
