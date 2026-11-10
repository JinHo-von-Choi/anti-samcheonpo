package cli

import (
	"bufio"
	"encoding/json"
	"os"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sockpath"
)

// missedSessions counts the sessions with no record because their hooks
// reached no daemon: those already in the ledger, and those still waiting for
// the daemon to read them.
func missedSessions(dbPath string) int {
	seen := map[string]bool{}
	for _, name := range []string{sockpath.MissLogPath(), sockpath.MissLogPath() + ".ingesting"} {
		f, err := os.Open(name)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			var rec struct {
				Agent   string `json:"agent"`
				Session string `json:"session_id"`
			}
			if json.Unmarshal(sc.Bytes(), &rec) == nil && rec.Agent != "" {
				seen[rec.Agent+"\x00"+rec.Session] = true
			}
		}
		f.Close()
	}
	n := len(seen)
	if db, err := ledger.OpenReadOnly(dbPath); err == nil {
		defer db.Close()
		if known, err := db.UnobservedSessions(); err == nil {
			n += known
		}
	}
	return n
}
