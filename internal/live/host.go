package live

import (
	"fmt"
	"sync"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/hostlimit"
)

// artifactDirs are the build output folders measured below a workspace root.
var artifactDirs = []string{"target", ".next", "node_modules/.cache", ".turbo", ".gradle", "build"}

const (
	hostMeasureEvery   = 5 * time.Minute
	hostMeasureBudget  = 2 * time.Second
	hostMeasureEntries = 200_000
	gb                 = 1_000_000_000
)

// hostState is the build output a workspace occupies, shared by the
// sessions working in it so a folder is walked once.
type hostState struct {
	measuring bool
	at        time.Time
	base      int64 // first complete measurement: the reference for growth
	baseSet   bool
	last      int64
	complete  bool // the latest measurement finished within its budget
}

type hostRegistry struct {
	mu sync.Mutex
	ws map[string]*hostState
}

// take starts a measurement of a workspace unless one runs or ran lately.
func (h *hostRegistry) take(key string, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ws == nil {
		h.ws = map[string]*hostState{}
	}
	st := h.ws[key]
	if st == nil {
		st = &hostState{}
		h.ws[key] = st
	}
	if st.measuring || !st.at.IsZero() && now.Sub(st.at) < hostMeasureEvery {
		return false
	}
	st.measuring = true
	return true
}

func (h *hostRegistry) done(key string, total int64, complete bool, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.ws[key]
	if st == nil {
		return
	}
	st.measuring, st.at, st.complete = false, now, complete
	if !complete {
		return
	}
	st.last = total
	if !st.baseSet {
		st.base, st.baseSet = total, true
	}
}

// growth is how much the build output grew since the first measurement;
// ok is false while no two complete measurements exist.
func (h *hostRegistry) growth(key string) (total, grown int64, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := h.ws[key]
	if st == nil || !st.baseSet || !st.complete {
		return 0, 0, false
	}
	return st.last, st.last - st.base, true
}

// hostSeen is a session's verification time and the host advice given.
type hostSeen struct {
	verifyMS       int64
	verifyUnknown  int
	artifactStep   int64
	verifyStep     int
	freeAdvisedPct int // free percentage at the last free-space advice (0: none)
}

// noteVerify counts an executed check and judges the host after it. A
// heavy check also starts a measurement of the build output. The caller
// holds s.mu.
func (s *Session) noteVerify(ev *event.Event) {
	if !detect.ExecutedVerify(ev) {
		return
	}
	if ev.DurationMS > 0 {
		s.host.verifyMS += ev.DurationMS
	} else {
		s.host.verifyUnknown++
	}
	if heavy, _ := s.eng.HeavyCheck(ev); heavy && s.daemon != nil {
		key, root := s.heavyKey(), s.Root
		if s.daemon.host.take(key, time.Now()) {
			go s.measureHost(key, root, ev)
		}
	}
	s.judgeHost(ev)
}

// measureHost walks the build output off the hook path and judges again.
func (s *Session) measureHost(key, root string, ev *event.Event) {
	total, err := hostlimit.Occupied(root, artifactDirs, time.Now().Add(hostMeasureBudget), hostMeasureEntries)
	s.daemon.host.done(key, total, err == nil, time.Now())
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.judgeHost(ev)
	}
}

// judgeHost advises on build output growth, free disk space and the time
// spent in checks, each once per step: every further HostArtifactGB of
// growth, every 5 points less free space, every 30 more minutes of checks.
// The caller holds s.mu.
func (s *Session) judgeHost(ev *event.Event) {
	cfg := s.Cfg.Detectors.S8
	var sigs []detect.Signal
	add := func(kind string, step int64, facts map[string]any) {
		// each kind and step is a fact of its own: the cooldown and the
		// one-prescription-per-cause rule of one do not hold the next
		key := fmt.Sprintf("host:%s:%d", kind, step)
		facts["kind"], facts["candidate_key"], facts["target"], facts["target_hash"] = kind, "host:"+kind, "host:"+kind, key
		sigs = append(sigs, detect.Signal{Detector: "S8", Rule: "s8.host_resource", Confidence: 0.9, Level: detect.L1, Evidence: []int64{ev.Seq}, Facts: facts})
	}
	if s.daemon != nil && cfg.HostArtifactGB > 0 {
		if total, grown, ok := s.daemon.host.growth(s.heavyKey()); ok {
			if step := grown / (int64(cfg.HostArtifactGB) * gb); step >= 1 && step > s.host.artifactStep {
				s.host.artifactStep = step
				add("artifacts", step, map[string]any{"total_gb": round1(total), "grown_gb": round1(grown), "dirs": "target, .next, node_modules/.cache, .turbo, .gradle, build"})
			}
		}
	}
	if cfg.HostFreePct > 0 || cfg.HostFreeGB > 0 {
		if d := hostlimit.SpaceOf(s.Root); d.Known {
			pct := int(d.FreeRatio()*100 + 0.5)
			low := cfg.HostFreePct > 0 && pct < cfg.HostFreePct || cfg.HostFreeGB > 0 && d.Free < uint64(cfg.HostFreeGB)*gb
			if low && (s.host.freeAdvisedPct == 0 || pct <= s.host.freeAdvisedPct-5) {
				s.host.freeAdvisedPct = max(pct, 1)
				add("free", int64(pct), map[string]any{"free_gb": round1(int64(d.Free)), "free_pct": pct})
			}
		}
	}
	if m := int(s.host.verifyMS / 60_000); cfg.HostVerifyMinutes > 0 && m >= cfg.HostVerifyMinutes {
		if step := 1 + (m-cfg.HostVerifyMinutes)/30; step > s.host.verifyStep {
			s.host.verifyStep = step
			add("verify_time", int64(step), map[string]any{"minutes": m, "unknown": s.host.verifyUnknown})
		}
	}
	if len(sigs) > 0 {
		s.deliver(s.eng.Emit(ev, sigs))
	}
}

// hostBudgetPre stops a heavy check once an accepted contract's build
// output or check time budget is spent. The caller holds s.mu.
func (s *Session) hostBudgetPre(ev *event.Event) *detect.Signal {
	if s.c == nil || s.acc.State != contract.StateAccepted || s.daemon == nil {
		return nil
	}
	b := s.c.Budget
	if b.ArtifactGB <= 0 && b.VerifyMinutes <= 0 {
		return nil
	}
	if heavy, _ := s.eng.HeavyCheck(ev); !heavy {
		return nil
	}
	facts := map[string]any{"cmd": ev.CmdNorm, "blocked": true}
	if b.ArtifactGB > 0 {
		if _, grown, ok := s.daemon.host.growth(s.heavyKey()); ok && grown >= int64(b.ArtifactGB)*gb {
			facts["kind"], facts["spent"], facts["budget"] = "artifacts", fmt.Sprintf("%.1fGB", round1(grown)), fmt.Sprintf("%dGB", b.ArtifactGB)
			return &detect.Signal{Detector: "S8", Rule: "s8.host_budget", Confidence: 1, Level: detect.L3, Evidence: []int64{ev.Seq}, Facts: facts}
		}
	}
	if m := int(s.host.verifyMS / 60_000); b.VerifyMinutes > 0 && m >= b.VerifyMinutes {
		facts["kind"], facts["spent"], facts["budget"] = "verify_time", fmt.Sprintf("%d분", m), fmt.Sprintf("%d분", b.VerifyMinutes)
		return &detect.Signal{Detector: "S8", Rule: "s8.host_budget", Confidence: 1, Level: detect.L3, Evidence: []int64{ev.Seq}, Facts: facts}
	}
	return nil
}

func round1(bytes int64) float64 { return float64(bytes*10/gb) / 10 }
