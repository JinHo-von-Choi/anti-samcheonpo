package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/seal"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/verification"
	"github.com/spf13/cobra"
)

type taskSpecInput struct {
	Spec       string                  `json:"spec"`
	TaskID     string                  `json:"task_id"`
	LegacyHead string                  `json:"legacy_head"`
	Revisions  []intent.Revision       `json:"revisions"`
	Evidence   []verification.Evidence `json:"evidence"`
	Seal       *seal.TaskSeal          `json:"seal,omitempty"`
}

func specTaskCmd() *cobra.Command {
	return &cobra.Command{Use: "task <input.json>", Short: "v1과 분리된 v2 작업 봉인 생성/검증 (명령 실행 없음)", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		f, err := os.Open(args[0])
		if err != nil {
			return err
		}
		defer f.Close()
		st, err := f.Stat()
		if err != nil {
			return err
		}
		if st.Size() > 4<<20 {
			return fmt.Errorf("task spec input exceeds 4 MiB")
		}
		decoder := json.NewDecoder(io.LimitReader(f, (4<<20)+1))
		decoder.DisallowUnknownFields()
		var input taskSpecInput
		if err := decoder.Decode(&input); err != nil {
			return err
		}
		if err := decoder.Decode(new(any)); err != io.EOF {
			return fmt.Errorf("trailing task spec data")
		}
		if input.Spec != seal.TaskSpecVersion {
			return fmt.Errorf("unsupported task spec")
		}
		if input.Seal != nil {
			if input.Seal.TaskID != input.TaskID || input.Seal.LegacyHead != input.LegacyHead {
				return fmt.Errorf("seal identity mismatch")
			}
			if err := seal.VerifyTask(*input.Seal, input.Revisions, input.Evidence); err != nil {
				return err
			}
		}
		result, err := seal.BuildTask(input.TaskID, input.LegacyHead, input.Revisions, input.Evidence)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}}
}
