package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/seal"
)

func TestSpecReportsContractVersionWithoutChangingLegacySeal(t *testing.T) {
	t.Setenv("SAMCHEONPO_HOME", t.TempDir())
	for _, version := range []string{contract.SpecVersion, contract.SpecVersion2, "progress-contract/999"} {
		t.Run(version, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "contract.yml")
			if err := os.WriteFile(p, []byte("spec: "+version+"\ngoal: inspect\ndone:\n  - check: 'true'\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cmd := specCmd()
			var output bytes.Buffer
			cmd.SetOut(&output)
			cmd.SetErr(&output)
			cmd.SetArgs([]string{"run", "../../testdata/claude/basic.jsonl", "--contract", p})
			err := cmd.Execute()
			if version == "progress-contract/999" {
				if err == nil {
					t.Fatal("unknown contract version accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got SpecOutput
			if err = json.Unmarshal(output.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.ContractSpec != version || got.Spec != seal.SpecVersion {
				t.Fatalf("wrong schema identity: %+v", got)
			}
		})
	}
}
