package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
)

// funnelReport is the JSON form of printFunnel.
type funnelReport struct {
	Since    time.Time              `json:"since"`
	Rules    []ledger.FunnelRow     `json:"rules"`
	Reproach *ledger.ReproachReport `json:"reproach,omitempty"`
}

// printFunnel shows, per rule, how many verdicts reached each step and why
// the others stopped, next to the reproach spans they fell in.
func printFunnel(cmd *cobra.Command, days int, out string) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()
	since := time.Now().AddDate(0, 0, -max(1, days))
	rows, err := db.DeliveryFunnel(since)
	if err != nil {
		return err
	}
	rep, err := db.ReproachSummary(since)
	if err != nil {
		return err
	}
	for i := range rows {
		rows[i].Reproach = rep.Matched[rows[i].Rule]
	}
	if out != "" {
		b, err := json.MarshalIndent(funnelReport{Since: since, Rules: rows, Reproach: rep}, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(out, append(b, '\n'), 0o600)
	}
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, lang.T("규칙\t판정\t기록만\t전달 대상\t전달\t차단\t효과 관측\t질책 일치\t전달 안 된 이유"))
	none, candidates := 0, 0
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%s\n", r.Rule, r.Verdicts, r.Observation, r.Candidates, r.Delivered, r.Blocked, r.Effect, r.Reproach, reasonList(r.Reasons))
		none += r.Reasons[ledger.ReasonNone]
		candidates += r.Candidates
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if candidates > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), lang.T("전달 대상 %d건 가운데 이유 없이 전달되지 않은 판정 %d건 (%.1f%%)\n"), candidates, none, 100*float64(none)/float64(candidates))
	}
	printReproach(cmd, rep)
	return nil
}

func reasonList(m map[string]int) string {
	type kv struct {
		k string
		v int
	}
	var all []kv
	for k, v := range m {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v || all[i].v == all[j].v && all[i].k < all[j].k })
	parts := make([]string, 0, len(all))
	for _, x := range all {
		parts = append(parts, fmt.Sprintf("%s %d", x.k, x.v))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}
