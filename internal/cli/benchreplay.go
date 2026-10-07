package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/bench"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sources"
)

// benchReplayCmd turns candidate spans from `samcheonpo gaps` into bench
// tasks restored from the original sessions. Spans that cannot be restored
// exactly are counted by reason.
func benchReplayCmd() *cobra.Command {
	var gapsPath, out, since string
	c := &cobra.Command{
		Use:   "replay",
		Short: "실제 세션의 의심 구간을 재현 과제로 만든다 (복원 불가 구간은 사유별로 센다)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if gapsPath == "" || out == "" {
				return fmt.Errorf("--gaps와 --out이 필요하다")
			}
			t, err := ParseSince(since, time.Now())
			if err != nil {
				return err
			}
			fh, err := os.Open(gapsPath)
			if err != nil {
				return err
			}
			var gaps []analyze.Gap
			sc := bufio.NewScanner(fh)
			sc.Buffer(make([]byte, 1<<20), 16<<20)
			for sc.Scan() {
				var g analyze.Gap
				if json.Unmarshal(sc.Bytes(), &g) == nil && g.Session != "" {
					gaps = append(gaps, g)
				}
			}
			fh.Close()
			if err := sc.Err(); err != nil {
				return err
			}
			files := map[string]sources.File{}
			for _, f := range sources.Find("claude", t, claudeDir(), codexDir()) {
				files[sourceSessionID(f)] = f
			}
			sessions := map[string]*event.Session{}
			reasons := map[string]int{}
			var made []bench.ReplayResult
			for _, g := range gaps {
				f, ok := files[g.Session]
				if !ok {
					reasons["원본 기록을 찾지 못했다"]++
					continue
				}
				s := sessions[g.Session]
				if s == nil {
					if s, err = sources.Parse(f); err != nil {
						reasons["원본 기록을 읽지 못했다"]++
						continue
					}
					sessions[g.Session] = s
				}
				r, err := bench.BuildReplay(s, f.Path, g.StartSeq, out)
				if err != nil {
					return err
				}
				if !r.Recoverable {
					reasons[r.Reason]++
					continue
				}
				made = append(made, r)
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "구간 %d개 중 재현 과제 %d개 생성 (%s)\n", len(gaps), len(made), out)
			keys := make([]string, 0, len(reasons))
			for k := range reasons {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(w, "  복원 불가 %3d  %s\n", reasons[k], k)
			}
			if len(made) > 0 {
				fmt.Fprintln(w, "생성된 과제는 채점기가 없어 독립 채점이 아니다. 채점 기준을 검토하고 grader/를 추가하기 전에는 효과 판정에 쓰지 않는다.")
			}
			return nil
		},
	}
	c.Flags().StringVar(&gapsPath, "gaps", "", "samcheonpo gaps --out으로 만든 표본 JSONL")
	c.Flags().StringVar(&out, "out", "", "과제를 만들 디렉터리 (원문이 들어가므로 저장소 밖 권장)")
	c.Flags().StringVar(&since, "since", "90d", "원본 기록을 찾을 기간")
	return c
}

// sourceSessionID is a Claude Code transcript's session ID: its file name.
func sourceSessionID(f sources.File) string {
	return strings.TrimSuffix(filepath.Base(f.Path), ".jsonl")
}
