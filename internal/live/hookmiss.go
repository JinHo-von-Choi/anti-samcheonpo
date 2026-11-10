package live

import (
	"bufio"
	"encoding/json"
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"
	"os"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sockpath"
)

// unknownSession holds the misses of hooks whose payload named no session.
const unknownSession = "unknown"

// ingestHookMisses moves the records of hooks that reached no daemon into
// the observation gaps. The file is renamed first, so a hook writing at the
// same time starts a new one, and removed only after the counts are saved.
func ingestHookMisses(db *ledger.DB) {
	path := sockpath.MissLogPath()
	work := path + ".ingesting"
	if _, err := os.Stat(work); err != nil && os.Rename(path, work) != nil {
		return
	}
	f, err := os.Open(work)
	if err != nil {
		return
	}
	type key struct{ agent, session string }
	counts := map[key]uint64{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var rec struct {
			Agent   string `json:"agent"`
			Session string `json:"session_id"`
		}
		if json.Unmarshal(sc.Bytes(), &rec) != nil || rec.Agent == "" {
			continue
		}
		if rec.Session == "" {
			rec.Session = unknownSession
		}
		counts[key{rec.Agent, rec.Session}]++
	}
	f.Close()
	for k, n := range counts {
		if err := db.AddObservationGap(k.agent, k.session, n); err != nil {
			fmt.Fprintf(os.Stderr, lang.T("samcheonpo: 훅 누락 기록 저장 실패: %v\n"), err)
			return
		}
	}
	_ = os.Remove(work)
}
