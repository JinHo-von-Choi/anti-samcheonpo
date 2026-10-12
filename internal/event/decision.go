package event

// Decision records why a candidate was not judged or delivered. Code and rule
// are controlled identifiers; no command, prompt or output is copied here.
type Decision struct {
	Rule     string  `json:"rule"`
	Code     string  `json:"code"`
	Evidence []int64 `json:"evidence,omitempty"`
}

type WorkProgress struct {
	Activity     bool   `json:"activity,omitempty"`
	Knowledge    bool   `json:"knowledge,omitempty"`
	Verification bool   `json:"verification,omitempty"`
	Completion   bool   `json:"completion,omitempty"`
	Basis        string `json:"basis,omitempty"`
}

type RequirementProgress struct {
	ID        string  `json:"id"`
	LabelHash string  `json:"label_hash"`
	Source    string  `json:"source"`
	State     string  `json:"state"`
	Evidence  []int64 `json:"evidence,omitempty"`
}
