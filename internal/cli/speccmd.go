package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/seal"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sources"
)

// SpecOutput is the conformance interface output (docs/spec).
type SpecOutput struct {
	Spec          string           `json:"spec"`
	ContractSpec  string           `json:"contract_spec"`
	ContractState string           `json:"contract_state"`
	Grade         string           `json:"grade"`
	Seal          seal.Seal        `json:"seal"`
	Rows          []seal.Row       `json:"rows"`
	Verdicts      []specVerdict    `json:"verdicts"`
	BucketTokens  map[string]int64 `json:"bucket_tokens"`
}

type specVerdict struct {
	Seq      int64  `json:"seq"`
	Detector string `json:"detector"`
	Rule     string `json:"rule"`
	Level    int    `json:"level"`
	Primary  bool   `json:"primary"`
	Estimate bool   `json:"estimate"`
}

func specCmd() *cobra.Command {
	c := &cobra.Command{Use: "spec", Short: "규격 적합성 인터페이스"}
	var agent, contractFile string
	run := &cobra.Command{
		Use:   "run <transcript>",
		Short: "기록 하나를 분석해 Evidence Ledger v1 결과를 JSON으로 낸다",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if agent == "" {
				agent = sources.DetectAgent(args[0])
			}
			s, err := sources.Parse(sources.File{Path: args[0], Agent: agent})
			if err != nil {
				return err
			}
			cfg := config.Default()
			cfg.Detectors.Overrides = map[string]int{}
			prices, err := loadPrices()
			if err != nil {
				return err
			}
			opt := analyze.Options{Config: cfg, ConfigHash: config.Hash(cfg), Prices: prices}
			state := string(contract.StateDraft)
			if contractFile != "" {
				b, err := os.ReadFile(contractFile)
				if err != nil {
					return err
				}
				k, errs := contract.Parse(b)
				if len(errs) > 0 {
					return fmt.Errorf("계약 오류: %v", errs)
				}
				opt.Contract, opt.Accepted = k, true
				state = string(contract.StateAccepted)
			}
			r, err := analyze.Run(s, opt)
			if err != nil {
				return err
			}
			if err := analyze.CheckInvariant(s, r.Totals); err != nil {
				return err
			}
			sl, rows := ledger.BuildSeal(r)
			out := SpecOutput{Spec: seal.SpecVersion, ContractSpec: contract.SpecVersion, ContractState: state, Grade: r.Grade, Seal: sl, Rows: rows,
				BucketTokens: r.Totals.BucketTokens}
			if opt.Contract != nil && opt.Contract.Spec != "" {
				out.ContractSpec = opt.Contract.Spec
			}
			for _, v := range r.Verdicts {
				out.Verdicts = append(out.Verdicts, specVerdict{v.Seq, v.Detector, v.Rule, int(v.Level), v.Primary, v.Estimate})
			}
			b, _ := json.MarshalIndent(out, "", " ")
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return nil
		},
	}
	run.Flags().StringVar(&agent, "agent", "", "claude | codex (기본: 경로로 추정)")
	run.Flags().StringVar(&contractFile, "contract", "", "수락된 계약 파일")
	c.AddCommand(run, specTaskCmd())
	return c
}
