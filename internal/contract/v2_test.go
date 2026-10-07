package contract

import (
	"os"
	"strings"
	"testing"
	"time"
)

const reusableContract = `spec: progress-contract/2
goal: fix input
done:
  - id: check
    check: test -s input
    pure: true
    reuse:
      inputs: [input]
      environment_files: [/usr/bin/dash]
      deterministic: true
      max_age_sec: 60
`

func TestV2ReuseIsExplicitAndAcceptanceBindsPolicy(t *testing.T) {
	c, errs := Parse([]byte(reusableContract))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	root := t.TempDir()
	os.MkdirAll(Dir(root), 0o700)
	os.WriteFile(Path(root), []byte(reusableContract), 0o600)
	if _, err := Accept(root, c, []byte(reusableContract), time.Now()); err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(reusableContract, "max_age_sec: 60", "max_age_sec: 600", 1)
	next, errs := Parse([]byte(changed))
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if c.ChecksHash() == next.ChecksHash() {
		t.Fatal("reuse policy missing from acceptance hash")
	}
	if state := CurrentState(root, next, []byte(changed)); state.State != StateStale {
		t.Fatal(state)
	}
	for _, raw := range []string{
		strings.Replace(reusableContract, SpecVersion2, SpecVersion, 1),
		strings.Replace(reusableContract, SpecVersion2, "progress-contract/999", 1),
		strings.Replace(reusableContract, "pure: true", "pure: false", 1),
		strings.Replace(reusableContract, "deterministic: true", "deterministic: false", 1),
		strings.Replace(reusableContract, "max_age_sec: 60", "max_age_sec: 0", 1),
		strings.Replace(reusableContract, "inputs: [input]", "inputs: [../outside]", 1),
	} {
		if _, errs := Parse([]byte(raw)); len(errs) == 0 {
			t.Fatal("invalid reuse accepted", raw)
		}
	}
}
