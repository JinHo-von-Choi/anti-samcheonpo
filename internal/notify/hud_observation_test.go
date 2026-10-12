package notify

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/assessment"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/verification"
	"testing"
	"time"
)

func TestHUDNewFieldsDoNotAliasCaller(t *testing.T) {
	now := time.Now()
	state := HUDState{Assessment: &assessment.Summary{Reasons: map[string]int{"missing_exit": 1}}, Work: &intent.Work{Remaining: []string{"report"}}, Verification: &verification.Plan{Items: []verification.PlanItem{{State: "candidate", ExpiresAt: &now}}}}
	p := BuildStateUpdate(state)
	state.Assessment.Reasons["missing_exit"] = 10
	state.Work.Remaining[0] = "changed"
	now = now.Add(time.Hour)
	state.Verification.Items[0].State = "pass"
	if p.State.Assessment.Reasons["missing_exit"] != 1 || p.State.Work.Remaining[0] != "report" || p.State.Verification.Items[0].State != "candidate" || p.State.Verification.Items[0].ExpiresAt.Equal(now) {
		t.Fatal("payload aliases mutable facts")
	}
}
