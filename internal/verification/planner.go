package verification

import "time"

// Candidate is a historical observation, not authority to skip a command.
// CurrentKey must come from a fresh input/environment fingerprint, not from
// copying Evidence.Key. Nil means that current validity remains unconfirmed.
type Candidate struct {
	CheckID    string
	Manual     bool
	Evidence   *Evidence
	CurrentKey *Key
}

type PlanItem struct {
	CheckID       string     `json:"check_id"`
	EvidenceID    string     `json:"evidence_id,omitempty"`
	SourceEventID string     `json:"source_event_id,omitempty"`
	ExpiresAt     *time.Time `json:"expires_at,omitempty"`
	State         string     `json:"state"`
	Reason        string     `json:"reason"`
}

type Plan struct {
	Items             []PlanItem `json:"items"`
	StopMachineChecks bool       `json:"stop_machine_checks"`
	ManualPending     int        `json:"manual_pending"`
}

// PlanChecks distinguishes an eligible past pass from a freshly validated
// reusable pass. It never promotes manual confirmation to automatic success.
func PlanChecks(candidates []Candidate, now time.Time) Plan {
	p := Plan{}
	machines, reusable := 0, 0
	for _, c := range candidates {
		i := PlanItem{CheckID: c.CheckID, State: "unconfirmed", Reason: "재사용 가능한 통과 근거가 없다"}
		if c.Manual {
			i.State, i.Reason = "manual", "수동 완료 조건은 별도 확인이 필요하다"
			p.ManualPending++
		} else {
			machines++
			if e := c.Evidence; e != nil && e.Key.CheckID == c.CheckID {
				if ok, _ := Reusable(*e, e.Key, now); ok {
					i.EvidenceID, i.SourceEventID = e.ID, e.SourceEventID
					expires := e.ExpiresAt
					i.ExpiresAt = &expires
					i.State, i.Reason = "candidate", "과거 통과 근거가 있으나 현재 입력·환경 일치는 아직 미확인이다"
					if c.CurrentKey != nil {
						if ok, _ := Reusable(*e, *c.CurrentKey, now); ok {
							i.State, i.Reason = "reusable", "현재 명령·입력·환경·개정과 일치하는 통과 근거가 있어 반복 실행이 필요하지 않다"
							reusable++
						} else {
							i.State, i.Reason = "invalidated", "현재 입력·환경·명령 또는 개정이 달라 이전 통과를 재사용할 수 없다"
						}
					}
				}
			}
		}
		p.Items = append(p.Items, i)
	}
	p.StopMachineChecks = machines > 0 && reusable == machines
	return p
}
