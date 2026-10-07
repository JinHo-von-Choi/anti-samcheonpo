package live

import (
	"fmt"
	"sync"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
)

// swarmRegistry ties sessions into lineage trees on explicit evidence only (a
// handoff link, or a parent a plugin names at session start) and bounds each
// tree that has a budget with one SwarmGovernor. A session with no lineage is
// its own tree and is never blocked here.
type swarmRegistry struct {
	mu       sync.Mutex
	parent   map[string]string // child -> parent
	includes map[string]bool   // session whose usage already includes its descendants
	trees    map[string]*SwarmGovernor
	limits   map[string]int64 // root -> limit in KRW
}

func (r *swarmRegistry) init() {
	if r.parent == nil {
		r.parent, r.includes, r.trees, r.limits = map[string]string{}, map[string]bool{}, map[string]*SwarmGovernor{}, map[string]int64{}
	}
}

// rootLocked walks the parent chain; a cycle stops at the first repeat.
func (r *swarmRegistry) rootLocked(id string) string {
	seen := map[string]bool{id: true}
	for {
		p, ok := r.parent[id]
		if !ok || p == "" || seen[p] {
			return id
		}
		seen[p] = true
		id = p
	}
}

// register records child under parent. limitKRW bounds the child's tree when
// the tree has no governor yet; 0 keeps lineage without a bound.
func (r *swarmRegistry) register(child, parent string, includesChildren bool, limitKRW int64) {
	if child == "" || parent == "" || child == parent {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	if _, known := r.parent[child]; !known {
		r.parent[child] = parent
	}
	if includesChildren {
		r.includes[child] = true
	}
	root := r.rootLocked(child)
	if _, has := r.trees[root]; !has && limitKRW > 0 {
		r.trees[root] = NewSwarmGovernor(limitKRW * 1_000_000)
		r.limits[root] = limitKRW
	}
	if g := r.trees[root]; g != nil {
		// register the chain top down so every node hangs under its parent
		var chain []string
		for id := child; id != ""; id = r.parent[id] {
			chain = append(chain, id)
			if id == root {
				break
			}
		}
		for i := len(chain) - 1; i >= 0; i-- {
			g.RegisterSession(chain[i], r.parent[chain[i]])
		}
	}
}

// record adds spend to the session's tree. Spend under a session whose usage
// already includes its descendants is not added twice.
func (r *swarmRegistry) record(id string, microKRW int64) {
	if microKRW <= 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	g := r.trees[r.rootLocked(id)]
	if g == nil {
		return
	}
	for p := r.parent[id]; p != ""; p = r.parent[p] {
		if r.includes[p] {
			return
		}
	}
	g.RecordSpend(id, microKRW)
}

// state reports the tree spend and limit of a bounded session.
func (r *swarmRegistry) state(id string) (spentKRW, limitKRW int64, blocked, bounded bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	root := r.rootLocked(id)
	g := r.trees[root]
	if g == nil {
		return 0, 0, false, false
	}
	return cost.Won(g.TreeTotalSpend(id)), r.limits[root], g.IsSessionBlocked(id), true
}

// treeLimitLocked is the budget for the tree a child joins: the user's swarm
// setting, else the accepted contract budget of the live parent. Requires d.mu.
func (d *Daemon) treeLimitLocked(child *Session, parent string) int64 {
	if child.Cfg.Swarm.TreeKRW > 0 {
		return child.Cfg.Swarm.TreeKRW
	}
	if p := d.sessions[parent]; p != nil {
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.c != nil && p.acc.State == contract.StateAccepted {
			return p.c.Budget.KRW
		}
	}
	return 0
}

// reportSpend forwards spend measured since the last report to the swarm
// registry. Called with s.mu held.
func (s *Session) reportSpend() {
	if s.daemon == nil {
		return
	}
	if delta := s.eng.St.TotalMicro - s.swarmReported; delta > 0 {
		s.swarmReported = s.eng.St.TotalMicro
		s.daemon.swarm.record(s.ID, delta)
	}
}

// treeSpend is the session's tree spend and limit when it is bounded. Called
// with s.mu held.
func (s *Session) treeSpend() (spent, limit int64, ok bool) {
	if s.daemon == nil {
		return 0, 0, false
	}
	spent, limit, _, ok = s.daemon.swarm.state(s.ID)
	return spent, limit, ok
}

// swarmPre refuses the next execution of a session whose lineage tree spent
// past its budget. The budget is an explicit guardrail, so the refusal does
// not wait for advice to be ignored. Called with s.mu held.
func (s *Session) swarmPre() *detect.Signal {
	if s.daemon == nil {
		return nil
	}
	s.reportSpend()
	spent, limit, blocked, ok := s.daemon.swarm.state(s.ID)
	if !ok || !blocked {
		return nil
	}
	pct := int64(0)
	if limit > 0 {
		pct = spent * 100 / limit
	}
	return &detect.Signal{Detector: "S8", Rule: "s8.budget", Confidence: 1.0, Level: detect.L3,
		Facts: map[string]any{"kind": "swarm_tree", "budget": contract.Comma(limit) + "원", "spent": contract.Comma(spent) + "원", "pct": pct,
			"cmd": "swarm", "blocked": true, "tree": fmt.Sprintf("부모·자식 세션을 합한 군집 지출 %s원이 한도 %s원을 넘었다", contract.Comma(spent), contract.Comma(limit))}}
}
