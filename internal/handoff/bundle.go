// Package handoff carries historical evidence, not execution authority.
package handoff

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/recovery"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/verification"
)

const Version = "handoff/1"
const MaxBytes = 4 << 20

type SessionRef struct {
	Agent string `json:"agent"`
	ID    string `json:"session_id"`
}

type FailedApproach struct {
	Seq         int64  `json:"seq"`
	Tool        string `json:"tool"`
	CommandHash string `json:"command_hash"`
	ResultHash  string `json:"result_hash"`
	Summary     string `json:"summary"`
	ExitCode    int    `json:"exit_code"`
}

type Bundle struct {
	Version        string                  `json:"version"`
	CreatedAt      time.Time               `json:"created_at"`
	Source         SessionRef              `json:"source"`
	Task           *intent.Task            `json:"task,omitempty"`
	Revisions      []intent.Revision       `json:"revisions"`
	Contract       *contract.Contract      `json:"contract,omitempty"`
	Evidence       []verification.Evidence `json:"evidence"`
	Recoveries     []recovery.Attempt      `json:"recoveries"`
	Failures       []FailedApproach        `json:"failed_approaches"`
	Sessions       []intent.SessionLink    `json:"sessions"`
	Usage          []Usage                 `json:"usage"`
	ObservationGap uint64                  `json:"observation_gap"`
	Unknown        []string                `json:"unknown"`
}

func (b Bundle) Validate() error {
	if b.Version != Version || b.CreatedAt.IsZero() || b.Source.Agent == "" || b.Source.ID == "" {
		return errors.New("invalid handoff version or source")
	}
	if b.Task == nil {
		if len(b.Revisions) > 0 || len(b.Evidence) > 0 || b.Contract != nil || len(b.Sessions) > 0 || len(b.Usage) > 0 {
			return errors.New("task-bound data without task identity")
		}
	} else {
		if b.Task.Version != intent.Version || b.Task.ID == "" || b.Task.ProjectID == "" || b.Task.CreatedAt.IsZero() || len(b.Revisions) == 0 {
			return errors.New("incomplete task history")
		}
		for i, r := range b.Revisions {
			if err := r.Validate(); err != nil {
				return err
			}
			if r.TaskID != b.Task.ID || r.Number != uint64(i+1) {
				return errors.New("discontinuous task history")
			}
		}
		latest := b.Revisions[len(b.Revisions)-1]
		if b.Contract != nil && (b.Contract.ChecksHash() != latest.ContractHash || b.Contract.Goal != latest.Goal) {
			return errors.New("contract does not match latest intent")
		}
		seen := map[string]bool{}
		for _, e := range b.Evidence {
			if err := e.Validate(); err != nil {
				return err
			}
			if e.Key.TaskID != b.Task.ID || e.Key.Revision != latest.Number || seen[e.ID] {
				return errors.New("invalid evidence ownership or duplicate")
			}
			seen[e.ID] = true
		}
		for _, link := range b.Sessions {
			if link.TaskID != b.Task.ID || link.Agent == "" || link.SessionID == "" {
				return errors.New("invalid session link")
			}
		}
	}
	for _, a := range b.Recoveries {
		if err := a.Validate(); err != nil {
			return err
		}
		if a.Agent != b.Source.Agent || a.SessionID != b.Source.ID {
			return errors.New("recovery belongs to another source")
		}
	}
	for _, f := range b.Failures {
		if f.Seq < 0 || f.Tool == "" || f.ExitCode <= 0 {
			return errors.New("failed approach needs an observed execution failure")
		}
	}
	return nil
}

func Decode(r io.Reader) (Bundle, error) {
	var b Bundle
	raw, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return b, err
	}
	if len(raw) > MaxBytes {
		return b, errors.New("handoff exceeds size limit")
	}
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if err := d.Decode(&b); err != nil {
		return b, err
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return b, errors.New("trailing handoff data")
	}
	return b, b.Validate()
}

func (b Bundle) Encode(w io.Writer) error {
	if err := b.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	if len(raw) > MaxBytes {
		return errors.New("handoff exceeds size limit; history was not truncated")
	}
	n, err := w.Write(append(raw, '\n'))
	if err == nil && n != len(raw)+1 {
		return io.ErrShortWrite
	}
	return err
}

// VerificationPlan only promotes a pass when the receiver independently
// supplies a matching current key. Imported keys alone remain historical.
func (b Bundle) VerificationPlan(current map[string]verification.Key, now time.Time) verification.Plan {
	if b.Contract == nil {
		return verification.Plan{}
	}
	var candidates []verification.Candidate
	for _, check := range b.Contract.Done {
		c := verification.Candidate{CheckID: check.ID, Manual: check.Manual != ""}
		if b.ObservationGap == 0 {
			for i := range b.Evidence {
				e := &b.Evidence[i]
				if e.Key.CheckID != check.ID {
					continue
				}
				if c.Evidence == nil || e.ObservedAt.After(c.Evidence.ObservedAt) {
					c.Evidence = e
				}
			}
			if key, ok := current[check.ID]; ok {
				k := key
				c.CurrentKey = &k
			}
		}
		candidates = append(candidates, c)
	}
	return verification.PlanChecks(candidates, now)
}

func (b Bundle) Text(current map[string]verification.Key, now time.Time) string {
	var out strings.Builder
	fmt.Fprintf(&out, "인수인계 출처: %s / %s\n이 묶음은 과거 기록이며 명령 실행·권한 확대 승인이 아닙니다.\n", b.Source.Agent, b.Source.ID)
	if len(b.Revisions) > 0 {
		r := b.Revisions[len(b.Revisions)-1]
		fmt.Fprintf(&out, "작업 %s · 개정 %d\n목표: %s\n요청 근거: %s / %s\n", r.TaskID, r.Number, r.Goal, r.Source.SessionID, r.Source.EventID)
	}
	if b.ObservationGap > 0 {
		fmt.Fprintf(&out, "관측 누락 %d건: 완료와 근거 재사용을 확정할 수 없습니다.\n", b.ObservationGap)
	}
	for _, item := range b.VerificationPlan(current, now).Items {
		fmt.Fprintf(&out, "- 조건 %s [%s]: %s (근거 %s, 출처 %s)\n", item.CheckID, item.State, item.Reason, item.EvidenceID, item.SourceEventID)
	}
	if b.Contract == nil {
		out.WriteString("완료 조건: 최신 목표와 연결된 계약을 확인하지 못했습니다.\n")
	} else {
		for _, c := range b.Contract.Done {
			if c.Manual != "" {
				fmt.Fprintf(&out, "  수동 조건 %s: %s\n", c.ID, c.Manual)
			} else {
				fmt.Fprintf(&out, "  조건 %s의 이전 검사 명령(실행 승인 아님): %s\n", c.ID, c.Check)
			}
		}
		fmt.Fprintf(&out, "이전 계약 참고 범위: %s\n보호 경로: %s\n금지: %s\n", strings.Join(b.Contract.Scope.Allow, ", "), strings.Join(b.Contract.Scope.Protect, ", "), strings.Join(b.Contract.Forbid, ", "))
	}
	for _, a := range b.Recoveries {
		fmt.Fprintf(&out, "- 이전 복구 [%s / %s]: %s\n  종료 조건: %s\n  결과 관찰: %s (전달·재발 없음은 해결 증명 아님)\n", a.Prescription.Cause, a.Stage, a.Prescription.Action, a.Prescription.StopCondition, a.Observation)
	}
	for _, f := range b.Failures {
		fmt.Fprintf(&out, "- 실패한 실행 event:%d [%s / exit %d]: %s (명령 지문 %s, 결과 지문 %s)\n", f.Seq, f.Tool, f.ExitCode, f.Summary, f.CommandHash, f.ResultHash)
	}
	for _, u := range b.Unknown {
		fmt.Fprintf(&out, "미확인: %s\n", u)
	}
	out.WriteString("이전 실패를 반복하기 전에 바뀐 근거를 확인하세요. 새 환경에서 확인하지 않은 통과는 미완료로 남깁니다.")
	if b.Task != nil {
		total := AggregateUsage(b.Task.ID, b.Sessions, b.Usage)
		if total.Tokens != nil {
			fmt.Fprintf(&out, "\n중복 제거한 연결 기록의 계측분: %d토큰 (총사용량의 완전성은 별도 확인)\n", *total.Tokens)
		}
		if total.APIEquivalentMicro != nil {
			fmt.Fprintf(&out, "계측분 API 환산액: %s원; 실제 청구액·절감액 아님\n", contract.Comma(cost.Won(*total.APIEquivalentMicro)))
		}
		for _, why := range total.Unknown {
			fmt.Fprintf(&out, "\n사용량 미확인: %s\n", why)
		}
	}
	return out.String()
}
