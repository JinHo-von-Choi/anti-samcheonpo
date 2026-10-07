package live

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/verification"
)

// verificationCandidates is called with s.mu held. It does no I/O on the hook
// path and therefore labels historical evidence as a candidate, never a current
// pass. The existing runner checks fresh fingerprints during checkpoint.
func (s *Session) verificationCandidates() verification.Plan {
	if s.c == nil || s.acc.State != contract.StateAccepted {
		return verification.Plan{}
	}
	var candidates []verification.Candidate
	taskID, revision := s.evidenceIdentity()
	for _, c := range s.c.Done {
		item := verification.Candidate{CheckID: c.ID, Manual: c.Manual != ""}
		if r, ok := s.checks[c.ID]; ok && r.Pass && !r.SideEffect && r.Skipped == "" && r.Evidence != nil && r.Evidence.Key.TaskID == taskID && r.Evidence.Key.Revision == revision {
			item.Evidence = r.Evidence
		}
		candidates = append(candidates, item)
	}
	return verification.PlanChecks(candidates, time.Now())
}

// evidenceIdentity is called with s.mu held. Legacy sessions without a task
// remain session-local; they cannot manufacture a durable task revision.
func (s *Session) evidenceIdentity() (string, uint64) {
	if s.task != nil && s.intentRevision != nil {
		return s.task.ID, s.intentRevision.Number
	}
	return s.ID, s.contractRevision
}

func verificationHint(p verification.Plan) string {
	var lines []string
	for _, i := range p.Items {
		if i.State == "candidate" {
			lines = append(lines, fmt.Sprintf("검사 %s: 재사용 후보 %s (출처 %s, 만료 %s). %s", i.CheckID, i.EvidenceID, i.SourceEventID, i.ExpiresAt.UTC().Format(time.RFC3339), i.Reason))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	lines = append(lines, "종료선: check에서 현재 입력·환경과 일치하는 통과가 확인되면 같은 검사를 반복하지 않는다. 목표 완료를 자동 선언하지 않는다.")
	if p.ManualPending > 0 {
		lines = append(lines, fmt.Sprintf("수동 완료 조건 %d개는 별도 확인이 필요하다.", p.ManualPending))
	}
	return strings.Join(lines, "\n")
}

// evidencePre flags a shell run of an accepted contract's pure, deterministic
// check whose passing evidence still matches the current inputs and
// environment. The key is recomputed here (bounded file reads); any
// mismatch, expiry, missing evidence or error lets the run through.
func (s *Session) evidencePre(cmd string, seq int64, deadline time.Time) *detect.Signal {
	norm, _ := fp.NormalizeCmd(cmd)
	if norm == "" {
		return nil
	}
	s.mu.Lock()
	if s.c == nil || s.acc.State != contract.StateAccepted || s.runner == nil {
		s.mu.Unlock()
		return nil
	}
	taskID, revision := s.evidenceIdentity()
	var check *contract.Check
	var evidence *verification.Evidence
	for _, c := range s.c.MachineChecks() {
		if c.Reuse == nil || !c.Pure {
			continue
		}
		if n, _ := fp.NormalizeCmd(c.Check); n != norm {
			continue
		}
		if r, ok := s.checks[c.ID]; ok && r.Pass && !r.SideEffect && r.Evidence != nil {
			cc, e := c, *r.Evidence
			check, evidence = &cc, &e
		}
		break
	}
	runner := s.runner
	s.mu.Unlock()
	if check == nil {
		return nil
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline.Add(-preMargin))
	defer cancel()
	key, err := runner.CurrentKeyContext(ctx, *check, taskID, revision)
	if err != nil {
		if ctx.Err() != nil {
			s.preLate.Add(1)
		}
		return nil
	}
	if ok, _ := verification.Reusable(*evidence, key, time.Now()); !ok {
		return nil
	}
	return &detect.Signal{Detector: "S1", Rule: "s1.evidence_rerun", Confidence: 0.95, Level: detect.L1, Evidence: []int64{seq},
		Facts: map[string]any{"cmd": norm, "check": check.ID, "evidence_id": evidence.ID, "expires": evidence.ExpiresAt.UTC().Format(time.RFC3339), "blocked": true}}
}
