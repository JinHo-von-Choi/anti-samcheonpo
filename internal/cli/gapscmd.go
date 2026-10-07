package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sources"
)

// gapsCmd samples spans the rules do not count as waste but that look like
// reported waste, for human labeling. It reads transcripts only and never
// writes the ledger.
func gapsCmd() *cobra.Command {
	var since, agent, out string
	var perPattern int
	c := &cobra.Command{
		Use:   "gaps",
		Short: "헛짓으로 분류되지 않은 의심 구간을 사람 판정용으로 뽑는다 (원장에 쓰지 않음)",
		RunE: func(cmd *cobra.Command, args []string) error {
			t, err := ParseSince(since, time.Now())
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
			var all []analyze.Gap
			failed := 0
			for _, f := range sources.Find(agent, t, claudeDir(), codexDir()) {
				r, err := analyzeFile(f, cfg, config.Hash(cfg), prices)
				if err != nil {
					failed++
					continue
				}
				all = append(all, analyze.Gaps(r)...)
			}
			byPattern := map[string][]analyze.Gap{}
			for _, g := range all {
				byPattern[g.Pattern] = append(byPattern[g.Pattern], g)
			}
			patterns := make([]string, 0, len(byPattern))
			for p := range byPattern {
				patterns = append(patterns, p)
			}
			sort.Strings(patterns)
			w := cmd.OutOrStdout()
			var sample []analyze.Gap
			for _, p := range patterns {
				gs := byPattern[p]
				var tokens int64
				for _, g := range gs {
					tokens += g.Tokens
				}
				fmt.Fprintf(w, "%-28s 구간 %5d개  토큰 %s\n", p, len(gs), commaInt(tokens))
				// largest spans first: they are where a missed rule costs most
				sort.SliceStable(gs, func(i, j int) bool { return gs[i].Tokens > gs[j].Tokens })
				sample = append(sample, gs[:min(perPattern, len(gs))]...)
			}
			if failed > 0 {
				fmt.Fprintf(w, "읽지 못한 기록 %d개\n", failed)
			}
			if out == "" {
				return nil
			}
			fh, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if err != nil {
				return err
			}
			bw := bufio.NewWriter(fh)
			for _, g := range sample {
				b, _ := json.Marshal(g)
				bw.Write(append(b, '\n'))
			}
			if err := bw.Flush(); err != nil {
				fh.Close()
				return err
			}
			if err := fh.Close(); err != nil {
				return err
			}
			fmt.Fprintf(w, "판정용 표본 %d개: %s (원문 프롬프트 일부가 들어 있어 공유하지 않는다)\n판정: samcheonpo label <session> 으로 해당 범위를 표시한다\n", len(sample), out)
			return nil
		},
	}
	c.Flags().StringVar(&since, "since", "30d", "기간")
	c.Flags().StringVar(&agent, "agent", "", "claude | codex (기본 전체)")
	c.Flags().StringVar(&out, "out", "", "표본 JSONL 경로 (새 파일만)")
	c.Flags().IntVar(&perPattern, "per-pattern", 20, "형태별 표본 수")
	return c
}
