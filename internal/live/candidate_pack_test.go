package live

import (
	"encoding/json"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rulepack"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstalledPackIsShadowUntilUserSelectsExactRule(t *testing.T) {
	for _, recommend := range []bool{false, true} {
		t.Run(map[bool]string{false: "shadow", true: "recommend"}[recommend], func(t *testing.T) {
			s, _ := reliabilitySession(t)
			d := rulepack.Definition{Symptom: "S4", Tool: event.ToolRead, Category: event.CatExplore, Minimum: 3, Window: 3}
			d.ID = rulepack.Digest(d)
			pack := rulepack.Pack{Version: "advisory-rulepack/1", Definitions: []rulepack.Definition{d}, ProposalDigest: "proposal", EvaluationDigest: "evaluation", Effectiveness: "unvalidated"}
			b, _ := json.Marshal(pack)
			if err := os.WriteFile(filepath.Join(config.Home(), "learned-rules.json"), b, 0600); err != nil {
				t.Fatal(err)
			}
			if recommend {
				if err := os.WriteFile(filepath.Join(config.Home(), "config.yml"), []byte("rollout:\n  recommend_rules: ["+d.RuleID()+"]\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			next, err := newSession("learned-child", "codex", s.Root, "", s.db, s.prices, adapter.Profiles["codex"].Caps)
			if err != nil {
				t.Fatal(err)
			}
			defer next.Finalize()
			next.onPrompt(HookInput{Prompt: "근거 조사"})
			next.mu.Lock()
			for i := 0; i < 3; i++ {
				ev := &event.Event{Kind: event.KindTool, Tool: event.ToolRead, Category: event.CatExplore, Paths: []string{"paper"}, Text: "same content"}
				next.parser.AddEvent(ev)
				next.observe(ev)
			}
			msgs := strings.Join(next.pending, "\n")
			if recommend && !strings.Contains(msgs, "평가") && !strings.Contains(msgs, "규칙 후보") {

				next.mu.Unlock()
				t.Fatalf("selected rule not delivered: verdicts=%+v", next.eng.Verdicts)
			}
			if !recommend && msgs != "" {
				next.mu.Unlock()
				t.Fatal("pack enabled unsolicited advice")
			}
			if next.unresolved != nil {
				next.mu.Unlock()
				t.Fatal("learned pack blocked")
			}
			next.mu.Unlock()
		})
	}
}
