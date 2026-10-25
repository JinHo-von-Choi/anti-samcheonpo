package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// provenanceRow is one rule's provenance: what the ledger records about it
// and a digest over those records. A provenance digest is reproducible only
// by someone who has the same ledger, so it is something a copied rule set
// cannot assert.
type provenanceRow struct {
	rule     string
	blocked  int
	attempts int
	feedback int
	first    string
	last     string
	sessions map[string]bool
	digest   string
	sections []string
}

type provenanceRuleJSON struct {
	Rule     string `json:"rule"`
	Blocked  int    `json:"blocked"`
	Attempts int    `json:"attempts"`
	Feedback int    `json:"feedback"`
	First    string `json:"first,omitempty"`
	Last     string `json:"last,omitempty"`
	Digest   string `json:"digest"`
}

// provenanceCmd prints per-rule evidence digests computed from this ledger.
func provenanceCmd() *cobra.Command {
	var format string
	c := &cobra.Command{
		Use:   "provenance [rule]",
		Short: "규칙별 원장 근거 다이제스트: 이 규칙이 이 원장에서 실측됐다는 증거",
		RunE: func(cmd *cobra.Command, args []string) error {
			blocks, err := openDB()
			if err != nil {
				return err
			}
			defer blocks.Close()
			db := blocks
			blockRows, err := db.BlockEvidence()
			if err != nil {
				return err
			}
			attempts, err := db.RecoveryAttemptsSince(time.Time{})
			if err != nil {
				return err
			}
			feedbackRows, err := db.FeedbackEvidence(time.Time{})
			if err != nil {
				return err
			}
			one := func(rule string) *provenanceRow {
				r := &provenanceRow{rule: rule, sessions: map[string]bool{}}
				return r
			}
			access := map[string]*provenanceRow{}
			for _, b := range blockRows {
				rule := b.Rule
				r := access[rule]
				if r == nil {
					r = one(rule)
					access[rule] = r
				}
				r.blocked += b.Count
				if b.Session != "" {
					r.sessions[b.Session] = true
				}
				r.sections = append(r.sections, fmt.Sprintf("block|%s|%s|%d", b.Agent, b.Session, b.Count))
			}
			for _, a := range attempts {
				rule := a.Rule
				if rule == "" {
					rule = "(규칙없음)"
				}
				r := access[rule]
				if r == nil {
					r = one(a.Rule)
					access[a.Rule] = r
				}
				r.attempts++
				r.sessions[a.SessionID] = true
				r.sections = append(r.sections, fmt.Sprintf("attempt|%s|%s|%s", a.Agent, a.SessionID, a.CreatedAt.UTC().Format("2006-01-02")))
				ts := a.CreatedAt.UTC().Format(time.RFC3339)
				if r.first == "" || ts < r.first {
					r.first = ts
				}
				if ts > r.last {
					r.last = ts
				}
			}
			for _, f := range feedbackRows {
				r := access[f.Rule]
				if r == nil {
					r = one(f.Rule)
					access[f.Rule] = r
				}
				r.feedback++
				r.sessions[f.Session] = true
				r.sections = append(r.sections, fmt.Sprintf("feedback|%s|%s|%s", f.Session, f.Label, f.At.UTC().Format("2006-01-02")))
				ts := f.At.UTC().Format(time.RFC3339)
				if r.first == "" || ts < r.first {
					r.first = ts
				}
				if ts > r.last {
					r.last = ts
				}
			}
			rules := make([]string, 0, len(access))
			for rule := range access {
				rules = append(rules, rule)
			}
			sort.Strings(rules)
			var rows []*provenanceRow
			if len(args) == 1 {
				r, ok := access[args[0]]
				if !ok {
					return fmt.Errorf("원장에 %s 규칙의 근거가 없다", args[0])
				}
				rows = []*provenanceRow{r}
			} else {
				for _, rule := range rules {
					rows = append(rows, access[rule])
				}
			}
			out := cmd.OutOrStdout()
			js := make([]provenanceRuleJSON, 0, len(rows))
			for _, r := range rows {
				r.digest = digestRule(r.rule, r.sections)
				js = append(js, provenanceRuleJSON{
					Rule: r.rule, Blocked: r.blocked, Attempts: r.attempts, Feedback: r.feedback,
					First: r.first, Last: r.last, Digest: r.digest,
				})
			}
			if format == "json" {
				return jsonEncode(out, js)
			}
			if len(args) == 1 {
				r := rows[0]
				fmt.Fprintf(out, "규칙: %s\n", r.rule)
				fmt.Fprintf(out, "차단 %d회, 처방 %d건, 피드백 %d건\n", r.blocked, r.attempts, r.feedback)
				fmt.Fprintf(out, "기간: %s ~ %s\n", dateOnly(r.first), dateOnly(r.last))
				fmt.Fprintf(out, "증거다이제스트: %s\n", r.digest)
				fmt.Fprintln(out, "근거 세션:")
				sessions := make([]string, 0, len(r.sessions))
				for s := range r.sessions {
					sessions = append(sessions, s)
				}
				sort.Strings(sessions)
				for i, s := range sessions {
					if i == 10 {
						fmt.Fprintf(out, "  … 외 %d개\n", len(sessions)-i)
						break
					}
					fmt.Fprintf(out, "  %s\n", s)
				}
				fmt.Fprintln(out, "다이제스트는 이 원장의 규칙별 증거 레코드를 정렬해 해시한 값이다. 같은 원장에서 samcheonpo provenance로 재계산해 비교하고, 다른 원장 간 규칙 신뢰도 비교에는 이 값을 쓴다.")
				return nil
			}
			fmt.Fprintf(out, "%-24s %5s %5s %6s %7s %s\n", "규칙", "차단", "처방", "피드백", "기간", "증거다이제스트")
			for _, r := range rows {
				fmt.Fprintf(out, "%-24s %5d %5d %6d %7s %s\n", r.rule, r.blocked, r.attempts, r.feedback, dateOnly(r.first), short(r.digest))
			}
			fmt.Fprintln(out, "기간은 처방·피드백이 처음·마지막으로 기록된 시각이다. 상세 보기: samcheonpo provenance <규칙>")
			return nil
		},
	}
	c.Flags().StringVar(&format, "format", "text", "text | json")
	return c
}

// digestRule hashes the rule name with its sorted evidence records. Insert
// order must not affect the digest, so records are sorted before hashing.
func digestRule(rule string, sections []string) string {
	s := append([]string{}, sections...)
	sort.Strings(s)
	h := sha256.New()
	h.Write([]byte(rule))
	h.Write([]byte{0})
	for _, line := range s {
		fmt.Fprintf(h, "%s\n", line)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func dateOnly(ts string) string {
	if len(ts) >= 10 {
		return ts[:10]
	}
	return "-"
}

// short truncates a provenance digest to 12 hex chars for the list view.
// The full digest is the detail view's authority; the list is only an aid
// for comparing rules against each other.
func short(digest string) string {
	s := strings.TrimPrefix(digest, "sha256:")
	trimmed := s
	if len(s) > 12 {
		trimmed = s[:12]
	}
	return "sha256:" + trimmed
}

func jsonEncode(w io.Writer, rules []provenanceRuleJSON) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(map[string]any{"rules": rules})
}
