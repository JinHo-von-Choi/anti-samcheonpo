package live

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"
	"sync"
	"time"
)

// ResourceClaim comes from a host adapter, never inferred from tool output.
// ID is hashed before storage. Shared read claims never conflict.
type ResourceClaim struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
	Mode string `json:"mode"`
}
type resourceLease struct {
	session, call, key, mode string
	at                       time.Time
}
type resourceRegistry struct {
	mu     sync.Mutex
	leases []resourceLease
}

// reserve atomically checks and records prospective calls. The caller releases
// a denied call, a completed call or the session. Missing results expire as
// unknown after 30 minutes, never as proof of completion or permission to kill.
func (r *resourceRegistry) reserve(session, call string, claims []ResourceClaim, now time.Time) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var kept []resourceLease
	for _, l := range r.leases {
		if now.Sub(l.at) < 30*time.Minute {
			kept = append(kept, l)
		}
	}
	r.leases = kept
	conflict := ""
	for _, c := range claims {
		if c.ID == "" || len(c.ID) > 2048 || c.Mode != "read" && c.Mode != "write" {
			continue
		}
		switch c.Kind {
		case "build_output", "database", "browser_session", "file":
		default:
			continue
		}
		key := fp.Hash(c.Kind, c.ID)
		duplicate := false
		for _, l := range r.leases {
			if l.key != key {
				continue
			}
			if l.session == session && l.call == call {
				duplicate = true
				continue
			}
			if c.Mode == "write" || l.mode == "write" {
				conflict = key
			}
		}
		if !duplicate {
			r.leases = append(r.leases, resourceLease{session, call, key, c.Mode, now})
		}
	}
	return conflict
}
func (r *resourceRegistry) release(session, call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.leases[:0]
	for _, l := range r.leases {
		if l.session != session || call != "" && l.call != call {
			kept = append(kept, l)
		}
	}
	r.leases = kept
}
func (s *Session) resourcePre(in HookInput, seq int64) *detect.Signal {
	if s.daemon == nil || len(in.Resources) == 0 || len(in.Resources) > 16 {
		return nil
	}
	key := s.daemon.resources.reserve(s.ID, in.ToolUseID, in.Resources, time.Now())
	if key == "" {
		return nil
	}
	return &detect.Signal{Detector: "S1", Rule: "s1.concurrent_shared_resource", Confidence: 1, Level: detect.L1, Evidence: []int64{seq}, Facts: map[string]any{"kind": "host_declared_resource", "target_hash": key, "next_action": lang.T("공유 자원 작업의 결과를 확인한 뒤 실행하거나 별도 자원을 지정한다")}}
}
