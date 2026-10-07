package seal

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/verification"
)

func TestTaskSealVersionIsolationAndTampering(t *testing.T) {
	now := time.Now().UTC()
	r, err := intent.Revise("task", nil, "private user request", intent.Source{Origin: intent.User, SessionID: "s", EventID: "e", At: now})
	if err != nil {
		t.Fatal(err)
	}
	revisions := []intent.Revision{r}
	e := verification.Evidence{Version: verification.Version, ID: "e", Key: verification.Key{TaskID: "task", Revision: 1, CheckID: "c", CommandHash: "cmd", InputHash: "input", EnvironmentHash: "env", RunnerVersion: "1"}, SessionID: "s", SourceEventID: "check", ObservedAt: now, ExpiresAt: now.Add(time.Hour), Pass: true, Complete: true, ResultHash: "pass"}
	proofs := []verification.Evidence{e}
	s, err := BuildTask("task", "legacy-head", revisions, proofs)
	if err != nil {
		t.Fatal(err)
	}
	if err = VerifyTask(s, revisions, proofs); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(s)
	if strings.Contains(string(b), r.Goal) {
		t.Fatal("raw goal exported")
	}
	for _, tc := range []string{"version", "legacy", "kind", "head", "payload"} {
		t.Run(tc, func(t *testing.T) {
			copySeal := s
			copySeal.Rows = append([]Row(nil), s.Rows...)
			copyRev := append([]intent.Revision(nil), revisions...)
			switch tc {
			case "version":
				copySeal.Spec = "future"
			case "legacy":
				copySeal.LegacyHead = "changed"
			case "kind":
				copySeal.Rows[0].Kind = "verification"
			case "head":
				copySeal.Head = "changed"
			case "payload":
				copyRev[0].Goal = "changed"
			}
			if err := VerifyTask(copySeal, copyRev, proofs); err == nil {
				t.Fatal("tampering accepted")
			}
		})
	}
	if SpecVersion != "evidence-ledger/1" {
		t.Fatal("v1 version changed")
	}
}
