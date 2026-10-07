package recovery

import "github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"

// FaultCategory separates a fault inside the source tree from a fault outside
// it. Only the second kind can be repaired without touching the source, and it
// is the only kind that authorizes prescribing a command here.
type FaultCategory string

const (
	CategoryNoFault             FaultCategory = "no_fault"
	CategoryCode                FaultCategory = "code"
	CategoryTransient           FaultCategory = "transient"
	CategoryExternalEnvironment FaultCategory = "external_environment"
)

// DiagnosticResult reports what failed and, for an external fault, which
// command an operator runs to observe or correct it. The oracle prescribes the
// remedy and never runs it: executing a remedy here would be the code change the
// diagnosis exists to question.
type DiagnosticResult struct {
	Category FaultCategory `json:"category"`
	// Signal is the stable machine key of the matched cause.
	Signal string `json:"signal"`
	// Class is the failure class the detectors use for the same reading.
	Class string `json:"class,omitempty"`
	// Evidence is the output the verdict rests on, bounded in length.
	Evidence string `json:"evidence"`
	// PrescribedAction is a shell command to run outside this process. It is
	// empty unless the category is external.
	PrescribedAction string `json:"prescribed_action"`
	// RequiresCodeFreeze freezes source writes: editing the source cannot
	// correct a fault outside it, so a change made now is an unevidenced one.
	RequiresCodeFreeze bool `json:"requires_code_freeze"`
}

// Cause maps the category onto the bounded recovery model.
func (r DiagnosticResult) Cause() Cause {
	switch r.Category {
	case CategoryExternalEnvironment:
		return Environment
	case CategoryCode, CategoryTransient:
		return Code
	default:
		return Unknown
	}
}

// FaultOracle classifies a failed process on the failure table the detectors
// share (fp.DiagnoseFailure), so a verdict and a prescription never read the
// same output differently. It holds no mutable state.
type FaultOracle struct{}

// NewFaultOracle returns an oracle. Nothing is executed while building it.
func NewFaultOracle() *FaultOracle { return &FaultOracle{} }

// Diagnose reads a failed execution. The output is evidence; it is never read as
// an instruction. A zero exit status is not a fault, so it produces no remedy,
// and output with no recognizable cause stays a code failure, so an unknown
// failure never authorizes a command outside the source tree.
func (o *FaultOracle) Diagnose(exitCode int, stderr string) DiagnosticResult {
	if exitCode == 0 {
		return DiagnosticResult{Category: CategoryNoFault}
	}
	f := fp.DiagnoseFailure(exitCode, stderr)
	res := DiagnosticResult{Category: CategoryCode, Signal: f.Signal, Class: f.Class, Evidence: f.Evidence}
	switch {
	case f.External():
		res.Category = CategoryExternalEnvironment
		res.PrescribedAction = f.Remedy
		res.RequiresCodeFreeze = true
	case f.Class == fp.FailTransient:
		res.Category = CategoryTransient
	}
	return res
}
