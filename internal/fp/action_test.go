package fp

import "testing"

func TestActionFingerprintRetainsNativeArguments(t *testing.T) {
	a := Action("Read", map[string]any{"file_path": "a", "offset": 1})
	b := Action("Read", map[string]any{"offset": 1, "file_path": "a"})
	if a == "" || a != b {
		t.Fatal("argument order should not change call identity")
	}
	if a == Action("Read", map[string]any{"file_path": "a", "offset": 200}) {
		t.Fatal("different read range merged")
	}
	if Action("Read", nil) != "" {
		t.Fatal("missing arguments fingerprinted as known")
	}
}
