package contract

import (
	"path/filepath"
	"strings"
)

// EvidenceSpec describes the accepted check's outcome and supporting artifacts.
// Artifacts are references, not automatic proof of correctness or authority.
type EvidenceSpec struct {
	Kind      string   `yaml:"kind" json:"kind"`
	Artifacts []string `yaml:"artifacts,omitempty" json:"artifacts,omitempty"`
}

func validateEvidenceSpec(spec string, c Check, field string) []ValidationError {
	var errs []ValidationError
	if spec != SpecVersion2 || c.Check == "" {
		errs = append(errs, ValidationError{field + ".evidence", "v2의 명시적 check에만 인수 근거를 연결할 수 있다"})
	}
	switch c.Evidence.Kind {
	case "automated_check", "document", "ui", "data", "api", "deployment":
	default:
		errs = append(errs, ValidationError{field + ".evidence.kind", "automated_check, document, ui, data, api, deployment 중 하나가 필요하다"})
	}
	if len(c.Evidence.Artifacts) > 16 || c.Evidence.Kind != "automated_check" && len(c.Evidence.Artifacts) == 0 {
		errs = append(errs, ValidationError{field + ".evidence.artifacts", "작업 유형의 구체적 근거 파일 1~16개가 필요하다"})
	}
	for _, p := range c.Evidence.Artifacts {
		if p == "" || p == "." || p == ".." || filepath.IsAbs(p) || strings.ContainsAny(p, "*?[\\:") || strings.HasPrefix(filepath.ToSlash(filepath.Clean(p)), "../") {
			errs = append(errs, ValidationError{field + ".evidence.artifacts", "프로젝트 안의 구체적인 파일 경로만 허용한다"})
		}
	}
	return errs
}
