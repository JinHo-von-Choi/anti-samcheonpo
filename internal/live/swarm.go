package live

import (
	"math"
	"sync"
)

// SwarmNode is one agent session inside a swarm lineage tree. Its identity and
// lineage are fixed at registration; spend and blocked state move afterwards.
// The accessor methods take the owning governor's lock, so a node may be read
// while other goroutines keep spending.
type SwarmNode struct {
	gov              *SwarmGovernor
	id, parentID     string
	blocked          bool
	spend, treeMicro int64
}

// ID is the session identifier this node was registered under.
func (n *SwarmNode) ID() string {
	if n == nil {
		return ""
	}
	return n.id
}

// ParentID is the registered parent session, empty for a tree root.
func (n *SwarmNode) ParentID() string {
	if n == nil {
		return ""
	}
	return n.parentID
}

// Blocked reports whether this session lost its right to spend.
func (n *SwarmNode) Blocked() bool {
	if n == nil || n.gov == nil {
		return false
	}
	n.gov.mu.RLock()
	defer n.gov.mu.RUnlock()
	return n.blocked
}

// SpendMicroKRW is the spend recorded for this session alone, excluding the
// spend of its descendants.
func (n *SwarmNode) SpendMicroKRW() int64 {
	if n == nil || n.gov == nil {
		return 0
	}
	n.gov.mu.RLock()
	defer n.gov.mu.RUnlock()
	return n.spend
}

// TreeMicroKRW is the spend of the whole lineage tree this node belongs to,
// including its own and every descendant's spend.
func (n *SwarmNode) TreeMicroKRW() int64 {
	if n == nil || n.gov == nil {
		return 0
	}
	n.gov.mu.RLock()
	defer n.gov.mu.RUnlock()
	return n.treeMicro
}

// SwarmGovernor keeps one shared budget for a tree of delegated sessions. A
// swarm may hand off work to children and siblings, so a per-session cap alone
// cannot bound the total the swarm burns: every session of a tree shares the
// tree's limit, and an overrun takes the whole tree down with it.
//
// A zero or negative limit blocks any tree as soon as it spends anything.
type SwarmGovernor struct {
	mu    sync.RWMutex
	limit int64
	nodes map[string]*SwarmNode
	order []string
}

// NewSwarmGovernor returns a governor that bounds every lineage tree by
// treeBudgetLimitMicroKRW. A negative limit is treated as zero.
func NewSwarmGovernor(treeBudgetLimitMicroKRW int64) *SwarmGovernor {
	if treeBudgetLimitMicroKRW < 0 {
		treeBudgetLimitMicroKRW = 0
	}
	return &SwarmGovernor{limit: treeBudgetLimitMicroKRW, nodes: map[string]*SwarmNode{}}
}

// RegisterSession records a session, and with a parentID its position in the
// lineage tree. A parent that has not registered yet is registered as its own
// tree root, so lineage is never silently lost and spend always lands in a
// tree. An empty sessionID cannot be governed and yields nil.
//
// Lineage is a fact, not a mutable hint: a session already registered keeps the
// parent it first declared. Re-registering an id returns that same node and
// never rewires the tree, which also makes cycles impossible.
func (g *SwarmGovernor) RegisterSession(sessionID, parentID string) *SwarmNode {
	if sessionID == "" {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.registerLocked(sessionID, parentID)
}

// RecordSpend accumulates microKRW on the session itself and on the tree total
// of the session and every ancestor of its lineage tree. An unknown session is
// registered as a root rather than dropped, because unaccounted spend is exactly
// what this governor exists to prevent. A negative amount is ignored: spend
// cannot be undone by a caller. Once the tree total passes the limit the whole
// tree, the offending session, its ancestors and its siblings alike, is blocked.
func (g *SwarmGovernor) RecordSpend(sessionID string, microKRW int64) {
	if sessionID == "" || microKRW < 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	node := g.registerLocked(sessionID, "")
	if node == nil {
		return
	}
	node.spend = addMicro(node.spend, microKRW)
	path := map[string]bool{node.id: true}
	for cur := node; cur != nil; {
		cur.treeMicro = addMicro(cur.treeMicro, microKRW)
		if cur.parentID == "" || path[cur.parentID] {
			break
		}
		path[cur.parentID] = true
		cur = g.nodes[cur.parentID]
	}
	if root := g.rootLocked(node.id); root != nil && root.treeMicro > g.limit {
		g.blockTreeLocked(root.id)
	}
}

// IsSessionBlocked reports whether the session may no longer spend. A tree that
// passed its limit blocks every session of that tree, so a swarm cannot keep
// spending by handing the work to a sibling. The check takes the write lock
// because the first observation of an overrun is what marks the tree blocked.
func (g *SwarmGovernor) IsSessionBlocked(sessionID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	node := g.nodes[sessionID]
	if node == nil {
		return false
	}
	if root := g.rootLocked(sessionID); root != nil && root.treeMicro > g.limit {
		g.blockTreeLocked(root.id)
	}
	return node.blocked
}

// TreeTotalSpend returns the total spend of the lineage tree the session
// belongs to, the session's own spend included. An unknown session has no
// measured total and reports zero rather than a guess.
func (g *SwarmGovernor) TreeTotalSpend(sessionID string) int64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	root := g.rootLocked(sessionID)
	if root == nil {
		return 0
	}
	return root.treeMicro
}

// Children returns the direct and deeper descendant sessions of sessionID in
// registration order. The session itself and sessions of other trees are never
// included, and an unknown session or a leaf yields an empty list.
func (g *SwarmGovernor) Children(sessionID string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	out := []string{}
	if _, ok := g.nodes[sessionID]; !ok {
		return out
	}
	for _, id := range g.order {
		if id == sessionID {
			continue
		}
		if root := g.rootLocked(id); root != nil && root.id == sessionID {
			out = append(out, id)
		}
	}
	return out
}

// registerLocked requires g.mu held.
func (g *SwarmGovernor) registerLocked(sessionID, parentID string) *SwarmNode {
	if node, ok := g.nodes[sessionID]; ok {
		return node
	}
	if parentID != "" && parentID != sessionID {
		if _, ok := g.nodes[parentID]; !ok {
			g.addLocked(parentID, "")
		}
		// A parent that already descends from this session cannot adopt it.
		if !g.isAncestorLocked(parentID, sessionID) {
			return g.addLocked(sessionID, parentID)
		}
	}
	return g.addLocked(sessionID, "")
}

// addLocked requires g.mu held and the id to be unused.
func (g *SwarmGovernor) addLocked(id, parentID string) *SwarmNode {
	node := &SwarmNode{gov: g, id: id, parentID: parentID}
	g.nodes[id] = node
	g.order = append(g.order, id)
	return node
}

// rootLocked walks the parent chain to the tree root. It tolerates a malformed
// chain so one bad lineage cannot hang the governor. Requires g.mu held.
func (g *SwarmGovernor) rootLocked(sessionID string) *SwarmNode {
	node, ok := g.nodes[sessionID]
	if !ok {
		return nil
	}
	path := map[string]bool{node.id: true}
	for node.parentID != "" {
		parent, ok := g.nodes[node.parentID]
		if !ok || path[parent.id] {
			return node
		}
		path[parent.id] = true
		node = parent
	}
	return node
}

// isAncestorLocked reports whether ancestor is ancestorID itself or one of its
// ancestors. Requires g.mu held.
func (g *SwarmGovernor) isAncestorLocked(ancestor, ancestorID string) bool {
	node, ok := g.nodes[ancestorID]
	if !ok {
		return false
	}
	path := map[string]bool{}
	for node != nil {
		if node.id == ancestor {
			return true
		}
		if path[node.id] {
			return false
		}
		path[node.id] = true
		if node.parentID == "" {
			return false
		}
		node = g.nodes[node.parentID]
	}
	return false
}

// blockTreeLocked blocks every session of one lineage tree. Requires g.mu held.
func (g *SwarmGovernor) blockTreeLocked(rootID string) {
	for _, id := range g.order {
		if root := g.rootLocked(id); root != nil && root.id == rootID {
			g.nodes[id].blocked = true
		}
	}
}

// addMicro adds a non-negative amount and saturates instead of wrapping, so an
// unrepresentable total blocks the tree rather than pretending it is small.
func addMicro(total, amount int64) int64 {
	if amount > 0 && total > math.MaxInt64-amount {
		return math.MaxInt64
	}
	return total + amount
}
