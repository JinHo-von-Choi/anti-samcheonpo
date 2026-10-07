package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/bench"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/receipt"
)

func labelCmd() *cobra.Command {
	var labeler, dir string
	var page int
	c := &cobra.Command{
		Use:   "label <session-id>",
		Short: "평가용 구간 표시",
		Long: `세션 이벤트를 요약으로 보여 주고 헛짓 구간을 표시한다.
명령: <시작> <끝> <증상 S1~S8|none> [메모] · x <번호> 원본 펼치기 · n 다음 쪽 · p 이전 쪽
      goal <목표> · success yes|no|partial|unknown · q 저장 후 종료`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if labeler == "" {
				return fmt.Errorf("--labeler 이름이 필요하다")
			}
			db, err := openDB()
			if err != nil {
				return err
			}
			defer db.Close()
			full, _, _, err := db.SourceOf(args[0])
			if err != nil {
				return fmt.Errorf("세션을 찾지 못했다: %w", err)
			}
			evs, err := db.EventsOf(full)
			if err != nil {
				return err
			}
			out := filepath.Join(dir, labeler, full+".jsonl")
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			var labels []eval.Label
			meta := &eval.Meta{Success: "unknown", Reviewed: true}
			if b, err := os.ReadFile(out); err == nil {
				for _, ln := range strings.Split(string(b), "\n") {
					var l eval.Label
					if json.Unmarshal([]byte(ln), &l) == nil {
						if l.Meta != nil {
							meta = l.Meta
						} else if l.Session != "" {
							labels = append(labels, l)
						}
					}
				}
			}
			w := cmd.OutOrStdout()
			in := bufio.NewScanner(cmd.InOrStdin())
			pg := 0
			show := func() {
				fmt.Fprintf(w, "\n세션 %s · 이벤트 %d개 · 쪽 %d/%d\n", short8(full), len(evs), pg+1, (len(evs)+page-1)/page)
				for i := pg * page; i < len(evs) && i < (pg+1)*page; i++ {
					ev := evs[i]
					ec := ""
					if ev.ExitCode != nil {
						ec = fmt.Sprintf(" [exit %d]", *ev.ExitCode)
					}
					fmt.Fprintf(w, "%5d %-8s %-8s %s%s\n", ev.Seq, ev.Category, ev.Bucket, trunc(ev.Summary, 100), ec)
				}
				fmt.Fprint(w, "> ")
			}
			show()
			for in.Scan() {
				line := strings.TrimSpace(in.Text())
				f := strings.Fields(line)
				switch {
				case line == "q":
					return saveLabels(out, full, labels, meta, w)
				case line == "n":
					if (pg+1)*page < len(evs) {
						pg++
					}
				case line == "p":
					if pg > 0 {
						pg--
					}
				case len(f) >= 2 && f[0] == "x":
					n, _ := strconv.ParseInt(f[1], 10, 64)
					for _, ev := range evs {
						if ev.Seq == n {
							fmt.Fprintln(w, expandSource(ev.SourceRef, 2000))
						}
					}
				case len(f) >= 2 && f[0] == "goal":
					meta.Goal = strings.TrimSpace(strings.TrimPrefix(line, "goal"))
				case len(f) == 2 && f[0] == "success":
					meta.Success = f[1]
				case len(f) >= 3:
					a, e1 := strconv.ParseInt(f[0], 10, 64)
					b, e2 := strconv.ParseInt(f[1], 10, 64)
					if e1 != nil || e2 != nil || b < a {
						fmt.Fprintln(w, "형식: <시작> <끝> <증상> [메모]")
						break
					}
					l := eval.Label{Session: full, Start: a, End: b, Symptom: f[2]}
					if len(f) > 3 {
						l.Note = strings.Join(f[3:], " ")
					}
					labels = append(labels, l)
					fmt.Fprintf(w, "표시함: %d-%d %s\n", a, b, f[2])
				}
				show()
			}
			return saveLabels(out, full, labels, meta, w)
		},
	}
	c.Flags().StringVar(&labeler, "labeler", "", "표시자 이름")
	c.Flags().StringVar(&dir, "dir", "labels", "표시 파일 디렉터리")
	c.Flags().IntVar(&page, "page", 40, "한 쪽 이벤트 수")
	return c
}

func saveLabels(out, sid string, labels []eval.Label, meta *eval.Meta, w io.Writer) error {
	var b strings.Builder
	m, _ := json.Marshal(eval.Label{Session: sid, Meta: meta})
	b.Write(m)
	b.WriteByte('\n')
	for _, l := range labels {
		j, _ := json.Marshal(l)
		b.Write(j)
		b.WriteByte('\n')
	}
	if err := writeAtomic(out, []byte(b.String())); err != nil {
		return err
	}
	fmt.Fprintf(w, "\n%s에 구간 %d개 저장\n", out, len(labels))
	return nil
}

// expandSource prints the original transcript line referenced by "path#offset".
func expandSource(ref string, n int) string {
	i := strings.LastIndexByte(ref, '#')
	if i < 0 {
		return "(원본 위치 없음)"
	}
	off, err := strconv.ParseInt(ref[i+1:], 10, 64)
	if err != nil {
		return "(원본 위치 형식 오류)"
	}
	f, err := os.Open(ref[:i])
	if err != nil {
		return "(이 장비에서 원본 기록을 열 수 없다)"
	}
	defer f.Close()
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return "(원본 위치를 찾지 못했다)"
	}
	rd := bufio.NewReaderSize(f, 1<<20)
	line, _ := rd.ReadString('\n')
	if len(line) > n {
		line = line[:n] + "…"
	}
	return line
}

func evalCmd() *cobra.Command {
	var labelsDir, splitFile, set, format string
	var interventions bool
	c := &cobra.Command{
		Use:   "eval",
		Short: "표시 데이터로 정밀도·재현율 계산",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openDB()
			if err != nil {
				return err
			}
			defer db.Close()
			w := cmd.OutOrStdout()
			if interventions {
				rep, err := InterventionReport(db)
				if err != nil {
					return err
				}
				return printJSONOr(w, format, rep, rep.Text())
			}
			if labelsDir == "" || splitFile == "" {
				return fmt.Errorf("--labels와 --split이 필요하다")
			}
			split, err := eval.LoadSplit(splitFile)
			if err != nil {
				return err
			}
			labels, err := eval.LoadLabels(labelsDir)
			if err != nil {
				return err
			}
			sessions := split.Holdout
			if set == "calibration" {
				sessions = split.Calibration
			}
			data := map[string]eval.SessionData{}
			for _, sid := range sessions {
				evs, err := db.EventsOf(sid)
				if err != nil {
					return err
				}
				vs, err := db.VerdictsOf(sid)
				if err != nil {
					return err
				}
				if len(evs) == 0 {
					return fmt.Errorf("세션 %s가 원장에 없다", sid)
				}
				data[sid] = eval.SessionData{Events: evs, Verdicts: vs}
			}
			rep := eval.Evaluate(set, sessions, data, labels)
			return printJSONOr(w, format, rep, evalText(rep))
		},
	}
	c.Flags().StringVar(&labelsDir, "labels", "", "표시 디렉터리 (labels/<표시자>/*.jsonl)")
	c.Flags().StringVar(&splitFile, "split", "", "보정·보류 세트 분할 JSON")
	c.Flags().StringVar(&set, "set", "holdout", "holdout | calibration")
	c.Flags().StringVar(&format, "format", "text", "text | json")
	c.Flags().BoolVar(&interventions, "interventions", false, "개입 실험 결과")
	return c
}

func printJSONOr(w io.Writer, format string, v any, text string) error {
	if format == "json" {
		b, _ := json.MarshalIndent(v, "", "  ")
		_, err := fmt.Fprintln(w, string(b))
		return err
	}
	_, err := fmt.Fprint(w, text)
	return err
}

func evalText(r eval.Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "평가 세트 %s · 세션 %d개 · 이중 표시 비율 %.0f%%\n", r.Set, r.Sessions, r.DoubleLabeledShare*100)
	fmt.Fprintf(&b, "%-14s %6s %18s %6s %18s %10s\n", "증상", "정밀도", "95% 구간", "재현율", "95% 구간", "지연 중앙값")
	row := func(s eval.SymptomScore) {
		name := receipt.SymptomNameOr(s.Symptom)
		flag := ""
		if s.Insufficient && s.Symptom != "all" && s.Symptom != "deterministic" {
			flag = "  표본 부족"
		}
		fmt.Fprintf(&b, "%s %5.0f%% %8.0f%%-%3.0f%% %5.0f%% %8.0f%%-%3.0f%% %9d원%s\n", padR(name, 14), s.Precision*100, s.PrecisionCI.Lo*100, s.PrecisionCI.Hi*100,
			s.Recall*100, s.RecallCI.Lo*100, s.RecallCI.Hi*100, s.LatencyMedianKRW, flag)
	}
	for _, s := range r.Symptoms {
		row(s)
	}
	o := r.Overall
	o.Symptom = "전체"
	row(o)
	d := r.Deterministic
	d.Symptom = "결정적"
	row(d)
	for _, k := range r.Kappa {
		fmt.Fprintf(&b, "표시자 일치도 %s-%s kappa %.2f (이벤트 %d)\n", k.A, k.B, k.Kappa, k.Events)
	}
	return b.String()
}

func padR(s string, n int) string {
	if d := n - receipt.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func benchCmd() *cobra.Command {
	var dir, format string
	var runs int
	c := &cobra.Command{
		Use:   "bench",
		Short: "ProgressBench 과제 실행",
		RunE: func(cmd *cobra.Command, args []string) error {
			sc, err := bench.Load(dir)
			if err != nil {
				return err
			}
			if len(sc) == 0 {
				return fmt.Errorf("%s에 과제가 없다", dir)
			}
			prices, err := loadPrices()
			if err != nil {
				return err
			}
			work, err := os.MkdirTemp("", "samcheonpo-bench-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(work)
			w := cmd.OutOrStdout()
			var outs []bench.Outcome
			fails := 0
			for _, s := range sc {
				var first bench.Outcome
				for i := 0; i < max(1, runs); i++ {
					o, err := bench.Run(s, prices, work)
					if err != nil {
						return fmt.Errorf("%s: %w", s.Name, err)
					}
					if i == 0 {
						first = o
					} else if strings.Join(o.Fired, ",") != strings.Join(first.Fired, ",") || o.WasteKRW != first.WasteKRW {
						first.Pass = false
						first.Problems = append(first.Problems, "반복 실행 결과가 다르다")
					}
				}
				if !first.Pass {
					fails++
				}
				outs = append(outs, first)
			}
			if format == "json" {
				b, _ := json.MarshalIndent(outs, "", "  ")
				fmt.Fprintln(w, string(b))
			} else {
				for _, o := range outs {
					st := "통과"
					if !o.Pass {
						st = "실패"
					}
					fmt.Fprintf(w, "%-4s %-34s %-6s %-8s L%d 헛짓 %6s원  %s\n", st, o.Name, o.Symptom, o.Kind, o.MaxLevel, commaInt(o.WasteKRW), strings.Join(o.Fired, " "))
					for _, p := range o.Problems {
						fmt.Fprintf(w, "       %s\n", p)
					}
				}
				fmt.Fprintf(w, "과제 %d개 중 %d개 통과\n", len(outs), len(outs)-fails)
			}
			if fails > 0 {
				return fmt.Errorf("과제 %d개 실패", fails)
			}
			return nil
		},
	}
	c.Flags().StringVar(&dir, "dir", "bench/scenarios", "과제 디렉터리")
	c.Flags().StringVar(&format, "format", "text", "text | json")
	c.Flags().IntVar(&runs, "runs", 2, "같은 과제 반복 실행 횟수 (결정성 확인)")
	c.AddCommand(benchABCmd(), benchReportCmd(), benchPlanCmd(), benchReplayCmd())
	return c
}

func commaInt(n int64) string {
	s := strconv.FormatInt(n, 10)
	var out []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 && s[i-1] != '-' {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}
