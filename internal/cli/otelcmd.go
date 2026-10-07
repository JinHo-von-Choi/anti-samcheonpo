package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/otel"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
)

func importOtelCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import-otel <otlp.jsonl>",
		Short: "OpenTelemetry GenAI 트레이스(OTLP/JSON)를 분석해 원장에 넣는다",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			sessions, err := otel.ParseFile(args[0])
			if err != nil {
				return err
			}
			cfg, _, err := config.Load("")
			if err != nil {
				return err
			}
			prices, err := loadPrices()
			if err != nil {
				return err
			}
			db, err := openDB()
			if err != nil {
				return err
			}
			defer db.Close()
			st, _ := os.Stat(args[0])
			for _, s := range sessions {
				r, err := analyze.Run(s, analyze.Options{Config: cfg, ConfigHash: config.Hash(cfg), Prices: prices})
				if err != nil {
					return err
				}
				if err := analyze.CheckInvariant(s, r.Totals); err != nil {
					return err
				}
				s.SourcePath = "" // one file holds several sessions; audit_source is per file
				if _, err := db.SaveAnalysis(r, st.Size(), st.ModTime().UnixNano(), Messages(s.FirstPrompt)); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s  %s  도구 %d회  판정 %d개\n", short8(s.ID), s.StartedAt.Local().Format(time.DateTime), s.ToolUses, len(r.Verdicts))
			}
			return nil
		},
	}
}
