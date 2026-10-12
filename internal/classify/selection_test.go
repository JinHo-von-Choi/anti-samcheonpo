package classify

import "testing"

func TestCheckSelectionKeepsActualScopeAndUnknownOptions(t *testing.T) {
	a := CheckSelection("pytest -q -k test_x")
	for _, cmd := range []string{"uv run pytest -v -k test_x", "python -m pytest -k test_x"} {
		if a != CheckSelection(cmd) {
			t.Fatalf("launcher/output variation changed advisory scope: %s", cmd)
		}
	}
	for _, cmd := range []string{"pytest -k test_y", "pytest tests/test_x.py", "pytest -k test_x -x", "pytest -k test_x --unknown=1"} {
		if a == CheckSelection(cmd) {
			t.Fatalf("selector/limit/unknown option dropped: %s", cmd)
		}
	}
}
