package cli

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/spf13/cobra"
)

func reportCmd() *cobra.Command {
	var since, mode, agent string
	c := &cobra.Command{Use: "report", Short: "에이전트별 관측·환산액·헛짓 분류 비교"}
	c.RunE = func(cmd *cobra.Command, _ []string) error {
		if mode != "all" && mode != "audit" && mode != "live" {
			return fmt.Errorf("mode는 all, audit 또는 live")
		}
		t, err := ParseSince(since, time.Now())
		if err != nil {
			return err
		}
		db, err := openDB()
		if err != nil {
			return err
		}
		defer db.Close()
		rows, err := db.Sessions(t, agent, "")
		if err != nil {
			return err
		}
		type group struct {
			agent, mode        string
			n, sources, checks int
			latest             time.Time
			activity           time.Time
			metadata           map[string]int
			t                  analyze.Totals
		}
		by := map[string]*group{}
		var ids []string
		for _, r := range rows {
			if mode == "all" || r.Mode == mode {
				ids = append(ids, r.ID)
			}
		}
		totals := db.BucketTotals(ids)
		for _, r := range rows {
			if mode != "all" && r.Mode != mode {
				continue
			}
			key := r.Agent + "/" + r.Mode
			g := by[key]
			if g == nil {
				g = &group{agent: r.Agent, mode: r.Mode, t: analyze.Totals{BucketTokens: map[string]int64{}, BucketMicro: map[string]int64{}, SymptomTokens: map[string]int64{}, SymptomMicro: map[string]int64{}}}
				by[key] = g
			}
			g.n++
			if g.metadata == nil {
				g.metadata = map[string]int{}
			}
			var reason, version, last string
			if err = db.QueryRow(`SELECT COALESCE(o.close_reason,'legacy_unknown'),COALESCE(o.evaluator_version,(SELECT json_extract(seal,'$.evaluator') FROM session_seal WHERE session_id=s.id),''),COALESCE((SELECT MAX(ts) FROM event WHERE session_id=s.id),o.last_activity_at,'') FROM session s LEFT JOIN session_observation o ON o.session_id=s.id WHERE s.id=?`, r.ID).Scan(&reason, &version, &last); err != nil {
				return err
			}
			labels := map[string]string{"legacy_unknown": "기존 기록·종료 확인 불가", "open": "관측 중", "session_end": "실제 종료 확인", "daemon_shutdown": "데몬 관측 종료", "audit_snapshot": "감사 스냅샷"}
			label := labels[reason]
			if label == "" {
				label = "종료 사유 미확인"
			}
			if version == "" {
				version = "판정기 버전 미확인"
			}
			g.metadata[label+" / "+version]++
			activity, _ := time.Parse(time.RFC3339Nano, last)
			if activity.After(g.activity) {
				g.activity = activity
			}
			if r.Source != "" {
				g.sources++
			}
			if r.Analyzed.After(g.latest) {
				g.latest = r.Analyzed
			}
			var observed int
			if err = db.QueryRow(`SELECT COUNT(*) FROM progress WHERE session_id=?`, r.ID).Scan(&observed); err != nil {
				return err
			}
			if observed > 0 {
				g.checks++
			}
			x := totals[r.ID]
			g.t.Micro += x.Micro
			g.t.Tokens += x.Tokens
			g.t.UnpricedTokens += x.UnpricedTokens
			for k, v := range x.BucketMicro {
				g.t.BucketMicro[k] += v
			}
			for k, v := range x.BucketTokens {
				g.t.BucketTokens[k] += v
			}
			for k, v := range x.SymptomMicro {
				g.t.SymptomMicro[k] += v
			}
			for k, v := range x.SymptomTokens {
				g.t.SymptomTokens[k] += v
			}
		}
		w := cmd.OutOrStdout()
		fmt.Fprintln(w, "에이전트 모드 세션 API환산액 기준 헛짓분류율 활동진척분류율 단가확인 원본 검사근거 최근분석")
		var keys []string
		for k := range by {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			g := by[k]
			x := g.t
			basis := "원화"
			amount := contract.Comma(cost.Won(x.Micro)) + "원"
			waste := x.SymptomTotal()
			progress := x.BucketMicro[analyze.BucketProgress]
			denom := x.Micro
			if x.Tokens == 0 {
				basis = "미확인"
				amount = "사용량 미확인"
			} else if x.UnpricedTokens > 0 {
				basis = "토큰"
				denom = x.Tokens
				waste = x.SymptomTokenTotal()
				progress = x.BucketTokens[analyze.BucketProgress]
				amount = "단가 미확인"
				if x.Micro > 0 {
					amount = contract.Comma(cost.Won(x.Micro)) + "원(확인된 일부 환산액)"
				}
			}
			pct := func(n int64) string {
				if denom == 0 || x.Tokens == 0 {
					return "미확인"
				}
				return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(denom))
			}
			coverage := "미확인"
			if x.Tokens > 0 {
				coverage = fmt.Sprintf("%.1f%%", 100*x.PriceCoverage())
			}
			checks := "완료 미확인"
			if g.checks > 0 {
				checks = fmt.Sprintf("검사관측%d/%d", g.checks, g.n)
			}
			at := "미확인"
			if !g.latest.IsZero() {
				at = g.latest.Local().Format("2006-01-02T15:04:05")
			}
			fmt.Fprintf(w, "%s %s %d %s %s %s %s %s %d/%d %s %s\n", g.agent, g.mode, g.n, amount, basis, pct(waste), pct(progress), coverage, g.sources, g.n, checks, at)
			activity := "미확인"
			if !g.activity.IsZero() {
				activity = g.activity.Local().Format("2006-01-02T15:04:05")
			}
			fmt.Fprintf(w, "  마지막 활동: %s; 관측 메타데이터:\n", activity)
			var metaKeys []string
			for k := range g.metadata {
				metaKeys = append(metaKeys, k)
			}
			sort.Strings(metaKeys)
			for _, k := range metaKeys {
				fmt.Fprintf(w, "    %s: %d\n", k, g.metadata[k])
			}
		}
		if len(keys) == 0 {
			fmt.Fprintln(w, "선택한 기간·모드의 기록이 없습니다. 미수집은 품질 0점이 아닙니다.")
		}
		fmt.Fprintln(w, "API환산액은 실제 청구액이 아닙니다. 활동 진척 분류와 검사 관측은 완료 증거가 아닙니다. 다른 기준·수집 범위의 행은 순위로 비교하지 않습니다.")
		if agent == "all" {
			var absent []string
			for _, a := range []string{"claude", "codex", "opencode", "agy", "hermes", "openclaw"} {
				found := false
				for _, g := range by {
					found = found || g.agent == a
				}
				if !found {
					absent = append(absent, a)
				}
			}
			if len(absent) > 0 {
				fmt.Fprintf(w, "선택 범위에 기록 없음: %s\n", strings.Join(absent, ", "))
			}
		}
		return nil
	}
	c.Flags().StringVar(&since, "since", "30d", "기간")
	c.Flags().StringVar(&mode, "mode", "all", "all | audit | live")
	c.Flags().StringVar(&agent, "agent", "all", "에이전트 또는 all")
	return c
}
