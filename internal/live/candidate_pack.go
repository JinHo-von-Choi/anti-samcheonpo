package live

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rulepack"
	"path/filepath"
)

// loadCandidatePack applies the learned rules to this session. A file that
// exists but cannot be used is recorded, not swallowed: without learned
// rules and with broken learned rules look identical to a reader who only
// sees that no rule fired.
func (s *Session) loadCandidatePack() {
	path := filepath.Join(config.Home(), "learned-rules.json")
	report := rulepack.LoadWithState(path)
	switch report.State {
	case rulepack.LoadAbsent:
		s.candidatePackState = rulepack.LoadAbsent
		return
	case rulepack.LoadUnreadable:
		s.candidatePackState = rulepack.LoadUnreadable
		return
	case rulepack.LoadInvalid:
		s.candidatePackState = rulepack.LoadInvalid
		return
	}
	if s.eng.ConfigureCandidates(report.Pack) == nil {
		s.candidatePackState = rulepack.LoadLoaded
		s.CfgHash = fp.Hash(s.CfgHash, rulepack.Digest(report.Pack))
		return
	}
	s.candidatePackState = rulepack.LoadInvalid
}
