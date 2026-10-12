package live

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rulepack"
	"path/filepath"
)

func (s *Session) loadCandidatePack() {
	p, err := rulepack.Load(filepath.Join(config.Home(), "learned-rules.json"))
	if err != nil {
		return
	}
	if s.eng.ConfigureCandidates(p) == nil {
		s.CfgHash = fp.Hash(s.CfgHash, rulepack.Digest(p))
	}
}
