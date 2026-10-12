package cli

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter/otel"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/assessment"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
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

func exportRedactedOTLP(cmd *cobra.Command, id, out string) error {
	db, err := ledger.OpenReadOnly(dbPath())
	if err != nil {
		return err
	}
	defer db.Close()
	full, _, agent, err := db.SourceOf(id)
	if err != nil {
		return err
	}
	evs, err := db.EventsOf(full)
	if err != nil {
		return err
	}
	vs, err := db.VerdictsOf(full)
	if err != nil {
		return err
	}
	channels, err := db.DeliveryChannels(full)
	if err != nil {
		return err
	}
	s := &event.Session{ID: full, Agent: agent, Events: evs}
	if len(evs) > 0 {
		s.StartedAt = evs[0].TS
		s.EndedAt = evs[len(evs)-1].TS
	}
	a := assessment.BuildRecorded(evs, vs, channels)
	b, err := otel.EncodeRedactedAssessment(s, a)
	if err != nil {
		return err
	}
	if out == "" {
		out = fp.Hash("conversation", full) + ".otlp.jsonl"
	}
	if err = writeAtomicMode(out, append(b, '\n'), 0600); err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), out)
	return err
}
