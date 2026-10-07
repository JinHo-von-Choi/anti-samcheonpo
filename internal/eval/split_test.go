package eval

import "testing"

func TestSplitRejectsDuplicatesAndFamilyLeakage(t *testing.T) {
	for _, kind := range []string{"duplicate", "empty", "family", "repository", "missing", "unknown"} {
		t.Run(kind, func(t *testing.T) {
			s := Split{Version: "split/2", Purpose: "preregistered_holdout", Calibration: []string{"a"}, Holdout: []string{"b"}, Groups: map[string]SplitGroup{"a": {Repository: "ra", Family: "fa"}, "b": {Repository: "rb", Family: "fb"}}}
			if err := s.Validate(); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "duplicate":
				s.Holdout = append(s.Holdout, "b")
			case "empty":
				s.Holdout = []string{""}
			case "family":
				s.Groups["b"] = SplitGroup{Repository: "rb", Family: "fa"}
			case "repository":
				s.Groups["b"] = SplitGroup{Repository: "ra", Family: "fb"}
			case "missing":
				delete(s.Groups, "b")
			case "unknown":
				s.Groups["unused"] = SplitGroup{Repository: "rx", Family: "fx"}
			}
			if err := s.Validate(); err == nil {
				t.Fatal("invalid split accepted")
			}
		})
	}
}
