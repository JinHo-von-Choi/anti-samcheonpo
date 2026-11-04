package cli

import (
	"encoding/json"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/verification"
	"github.com/spf13/cobra"
)

func evidenceCmd() *cobra.Command {
	var session string
	c := &cobra.Command{Use: "evidence", Short: "호스트 검사 관측과 요구사항 상태 조회 (자동 완료 아님)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		db, err := ledger.OpenReadOnly(dbPath())
		if err != nil {
			return err
		}
		defer db.Close()
		events, err := db.EventsOf(session)
		if err != nil {
			return err
		}
		type row struct {
			Seq          int64                     `json:"seq"`
			Source       string                    `json:"source"`
			Verification *verification.Observation `json:"verification"`
		}
		checks := []row{}
		var requirements []event.RequirementProgress
		for _, ev := range events {
			if ev.Verification != nil {
				checks = append(checks, row{ev.Seq, ev.SourceRef, ev.Verification})
			}
			if len(ev.Requirements) > 0 {
				requirements = ev.Requirements
			}
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{"session_id": session, "checks": checks, "requirements": requirements, "completion": "not_inferred", "reusable_authority": false})
	}}
	c.Flags().StringVar(&session, "session", "", "전체 세션 ID")
	c.MarkFlagRequired("session")
	return c
}
