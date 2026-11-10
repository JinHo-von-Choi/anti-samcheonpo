// Package recovery chooses a bounded next action, never executes it.
package recovery

import (
	"errors"
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"strings"
	"time"
)

type Cause string

const (
	Verification Cause = "verification_repeat"
	Code         Cause = "code_failure"
	Environment  Cause = "environment"
	Scope        Cause = "scope"
	Capability   Cause = "capability"
	Memory       Cause = "memory"
	Budget       Cause = "budget"
	Process      Cause = "process_cleanup"
	Writing      Cause = "writing"
	Unknown      Cause = "unknown"
)

type Prescription struct {
	Cause         Cause  `json:"cause"`
	Action        string `json:"action"`
	StopCondition string `json:"stop_condition"`
	MaxAttempts   int    `json:"max_attempts"`
	Handoff       bool   `json:"handoff"`
	// Shell is the remedy for an external fault, to be run by an operator and
	// not by the agent. It is empty unless the cause is the environment.
	Shell string `json:"shell,omitempty"`
}

// causeByRule names the cause of the rules whose prefix says too little: an
// S5 rule can be about verification, code or writing, and an S8 rule is not
// always about money.
var causeByRule = map[string]Cause{
	"s5.false_done":                Verification,
	"s5.abandoned_verification":    Verification,
	"s5.stop_unmet":                Verification,
	"s5.partial_done":              Verification,
	"s5.deploy_without_preflight":  Verification,
	"s5.release_without_preflight": Verification,
	"s5.release_rate":              Verification,
	"s5.buried_remainder":          Verification,
	"s5.unobserved_result":         Verification,
	"s5.critique_required":         Verification,
	"s5.scope_dropped":             Scope,
	"s5.error_hiding":              Code,
	"s5.test_weakening":            Code,
	"s5.flake_masking":             Code,
	"s5.artifact_meta":             Writing,
	"s5.answer_padding":            Writing,
	"s3.unrequested_ui_action":     Scope,
	"s8.leftover_process":          Process,
	"s8.host_resource":             Environment,
}

// Diagnose needs a failed execution before interpreting an environmental
// error. Prose alone, including a user/tool instruction, is not that evidence.
func Diagnose(rule, errorText string, failed bool) Cause {
	if rule == "s2.environment" {
		return Environment
	}
	if failed && fp.External(fp.ClassifyFailure(errorText)) {
		return Environment
	}
	if cause, ok := causeByRule[rule]; ok {
		return cause
	}
	switch {
	case strings.HasPrefix(rule, "s1."):
		return Verification
	case strings.HasPrefix(rule, "s2."):
		return Code
	case strings.HasPrefix(rule, "s3."):
		return Scope
	case strings.HasPrefix(rule, "s6."):
		return Capability
	case strings.HasPrefix(rule, "s7."):
		return Memory
	case strings.HasPrefix(rule, "s8."):
		return Budget
	default:
		return Unknown
	}
}

// DiagnoseExecution relates a failed execution to the bounded recovery model.
// The oracle names the category and the evidence, Diagnose names the cause. An
// observed external fault is always the environment cause, whatever the rule
// says: the rule is a prior, and a failed run is the stronger evidence.
func DiagnoseExecution(rule string, exitCode int, stderr string) (DiagnosticResult, Cause) {
	res := NewFaultOracle().Diagnose(exitCode, stderr)
	switch res.Category {
	case CategoryNoFault:
		// Nothing failed, so there is no failed execution to read a cause from.
		return res, Diagnose(rule, "", false)
	case CategoryExternalEnvironment:
		return res, Environment
	default:
		return res, Diagnose(rule, "", true)
	}
}

// Prescribe joins the oracle's freeze to the bounded prescription. An external
// fault overrides the cause it was handed, because no code edit is a valid
// response to a fault outside the code.
func Prescribe(cause Cause, res DiagnosticResult) Prescription {
	if res.Category == CategoryExternalEnvironment {
		cause = Environment
	}
	p := For(cause)
	if res.Category != CategoryNoFault {
		p.Shell = res.PrescribedAction
	}
	return p
}

// The prescription that replaces a repeated one.
const (
	HandoffAction = "앞서 제안한 처방을 반복하지 않는다. 관측한 실패·시도·미확인 조건을 묶어 사용자에게 인수인계한다."
	HandoffStop   = "새 정보나 명시적 사용자 선택이 있어야 다음 전략을 시작한다."
)

func For(cause Cause) Prescription {
	p := Prescription{Cause: cause, MaxAttempts: 1}
	switch cause {
	case Environment:
		p.Action = "같은 코드 수정을 멈추고 관측된 권한·명령·서비스 장애와 필요한 외부 조치를 사용자에게 요약한다. 권한·설정·모델을 임의로 바꾸지 않는다."
		p.StopCondition = "외부 조건 변경이 확인되기 전에는 같은 실행을 반복하지 않는다."
		p.Handoff = true
	case Verification:
		p.Action = "이미 통과한 검사의 명령·입력·환경·목표 개정을 확인한다. 유효한 근거가 있으면 재사용하고, 바뀐 범위 또는 아직 미확인인 조건만 검사한다."
		p.StopCondition = "수락된 완료 조건에 충분한 근거가 생기면 검사를 끝낸다. 수동 조건은 별도로 남긴다."
	case Code:
		p.Action = "같은 오류와 실패한 접근을 묶고 새로운 근거가 있는 가설 하나만 좁은 검사로 확인한다. 근거 없이 파일과 명령을 바꾸며 재시도하지 않는다."
		p.StopCondition = "같은 처방이 실패하거나 새 근거가 없으면 인수인계하고 수정 반복을 멈춘다."
	case Scope:
		p.Action = "최신 사용자 요청과 현재 변경의 관계를 확인한다. 범위 확대가 필요한 경우 변경 전 선택지를 사용자에게 설명한다."
		p.StopCondition = "명시적 방향 변경은 반영하되 승인되지 않은 범위 확대는 진행하지 않는다."
	case Capability:
		p.Action = "실패한 접근과 미확인 가정을 요약하고 작업 분할·추가 정보 요청·전략 변경 중 근거 있는 선택지를 제시한다. 모델을 자동 교체하지 않는다."
		p.StopCondition = "새 정보나 사용자 선택 없이 같은 전략을 반복하지 않는다."
		p.Handoff = true
	case Memory:
		p.Action = "최신 목표·검증 근거·이미 실패한 접근을 인수인계 자료로 묶는다. 과거의 통과를 새 환경의 통과로 취급하지 않는다."
		p.StopCondition = "실패한 접근을 다시 시작하기 전에 달라진 조건을 확인한다."
		p.Handoff = true
	case Process:
		p.Action = "남아 있는 프로세스가 이 세션이 띄운 것인지 확인한다. 더 쓰지 않을 프로세스만 정리하고, 남겨 둘 프로세스는 이유를 사용자에게 알린다."
		p.StopCondition = "이 세션이 띄웠다는 근거가 없는 프로세스는 종료하지 않는다."
	case Writing:
		p.Action = "후보 문장을 지우거나 대상 자체를 설명하는 문장으로 고친다. 지워도 독자가 얻는 내용이 그대로인지 확인한다."
		p.StopCondition = "독자에게 필요한 내용을 지워야 한다면 후보를 그대로 두고 사용자에게 판단을 맡긴다."
	case Budget:
		p.Action = "명시적 예산과 지금까지 관측한 사용량·미확인 비용을 요약한다. 추가 실행이 한도를 넘으면 사용자에게 선택을 요청한다."
		p.StopCondition = "예산 확대나 새 목표 없이 자동 계속하기를 반복하지 않는다."
		p.Handoff = true
	default:
		p.Action = "관측 사실과 미확인 원인을 분리하고 가장 작은 확인 한 번으로 다음 행동을 정한다."
		p.StopCondition = "확인 뒤에도 원인이 불명확하면 추측성 수정을 이어가지 않는다."
	}
	return p
}

type Stage string

const (
	Proposed       Stage = "proposed"
	Emitted        Stage = "emitted"
	Delivered      Stage = "delivered"
	Acknowledged   Stage = "acknowledged"
	EffectObserved Stage = "effect_observed"
	Censored       Stage = "censored"
)

type Attempt struct {
	PolicyVersion         string `json:"policy_version,omitempty"`
	Experiment            string `json:"experiment,omitempty"`
	ObservationBasis      string `json:"observation_basis,omitempty"`
	TargetHash            string `json:"target_hash,omitempty"`
	RelevantOpportunities int    `json:"relevant_opportunities,omitempty"`
	DeliveryReason        string `json:"delivery_reason,omitempty"`
	// Arm is versioned by Experiment. Legacy none was withheld; wording/2
	// none is a fact-only alias and requires an actual delivery receipt.
	Arm string `json:"arm,omitempty"`

	Version         string       `json:"version"`
	ID              string       `json:"id"`
	Agent           string       `json:"agent"`
	SessionID       string       `json:"session_id"`
	Revision        uint64       `json:"revision"`
	CauseKey        string       `json:"cause_key"`
	VerdictID       string       `json:"verdict_id"`
	Rule            string       `json:"rule"`
	Seq             int64        `json:"seq"`
	Prescription    Prescription `json:"prescription"`
	Stage           Stage        `json:"stage"`
	CreatedAt       time.Time    `json:"created_at"`
	EmittedAt       *time.Time   `json:"emitted_at,omitempty"`
	DeliveredAt     *time.Time   `json:"delivered_at,omitempty"`
	DeliveredSeq    int64        `json:"delivered_seq,omitempty"`
	AcknowledgedAt  *time.Time   `json:"acknowledged_at,omitempty"`
	Observation     string       `json:"observation,omitempty"`
	ObservedThrough int64        `json:"observed_through,omitempty"`
	Route           string       `json:"route"`
}

func (a Attempt) Validate() error {
	if a.Version != "recovery/1" || a.ID == "" || a.Agent == "" || a.SessionID == "" || a.CauseKey == "" || a.VerdictID == "" || a.CreatedAt.IsZero() {
		return errors.New("incomplete recovery identity")
	}
	switch a.Stage {
	case Proposed, Emitted, Delivered, Acknowledged, EffectObserved, Censored:
	default:
		return errors.New("unknown recovery stage")
	}
	if a.Stage == Emitted && a.EmittedAt == nil {
		return errors.New("emission evidence missing")
	}
	if (a.Stage == Delivered || a.Stage == Acknowledged || a.Stage == EffectObserved) && (a.DeliveredAt == nil || a.EmittedAt == nil) {
		return errors.New("delivery evidence missing")
	}
	if a.Stage == Acknowledged && a.AcknowledgedAt == nil {
		return errors.New("acknowledgment missing")
	}
	if a.Stage == EffectObserved && a.Observation == "" {
		return errors.New("observation missing")
	}
	return nil
}

func (a *Attempt) Advance(next Stage, now time.Time) error {
	if now.Before(a.CreatedAt) {
		return errors.New("invalid recovery time")
	}
	for _, at := range []*time.Time{a.EmittedAt, a.DeliveredAt, a.AcknowledgedAt} {
		if at != nil && now.Before(*at) {
			return errors.New("recovery transition time moved backwards")
		}
	}
	if a.Stage == next {
		return nil
	}
	allowed := next == Censored && a.Stage != EffectObserved
	switch {
	case a.Stage == Proposed && next == Emitted:
		allowed = true
	case a.Stage == Emitted && next == Delivered:
		allowed = true
	case a.Stage == Delivered && next == Acknowledged:
		allowed = true
	case (a.Stage == Delivered || a.Stage == Acknowledged) && next == EffectObserved && a.Observation != "":
		allowed = true
	case a.Stage == Proposed && a.Arm == "none" && next == EffectObserved && a.Observation != "":
		allowed = true
	}
	if !allowed {
		return fmt.Errorf("invalid recovery transition: %s -> %s", a.Stage, next)
	}
	now = now.UTC()
	switch next {
	case Emitted:
		a.EmittedAt = &now
	case Delivered:
		a.DeliveredAt = &now
	case Acknowledged:
		a.AcknowledgedAt = &now
	}
	a.Stage = next
	return nil
}

// Select imposes one prescription per cause and three proposals per revision.
// Once one attempt exists, a single handoff may replace further prescriptions.
// Proposals reserve the budget even if delivery remains unknown.
func Select(cause Cause, key string, revision uint64, history []Attempt) (Prescription, bool) {
	count, same, handoff := 0, 0, false
	for _, a := range history {
		if a.Revision != revision {
			continue
		}
		count++
		if a.CauseKey == key {
			same++
			handoff = handoff || a.Prescription.Handoff
		}
		if a.CauseKey == key && (a.Stage == Proposed || a.Stage == Emitted || a.Stage == Delivered || a.Stage == Acknowledged) {
			return Prescription{}, false
		}
	}
	if count >= 3 || handoff {
		return Prescription{}, false
	}
	p := For(cause)
	if same > 0 {
		p.Handoff = true
		p.Action = HandoffAction
		p.StopCondition = HandoffStop
	}
	return p, true
}
