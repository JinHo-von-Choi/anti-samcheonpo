package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/uicheck"
)

func uiCheckCmd() *cobra.Command {
	var format string
	var timeout time.Duration
	c := &cobra.Command{
		Use:   lang.T("ui-check <파일 또는 로컬 주소>"),
		Short: lang.T("브라우저에서 버튼과 링크를 눌러 동작이 없는 것을 찾는다"),
		Long:  lang.T("설치된 Chrome 계열 브라우저로 페이지를 열어 클릭 대상을 하나씩 누른다. 파일과 이 컴퓨터의 주소만 검사하고, 삭제·결제처럼 되돌리기 어려워 보이는 라벨과 다른 사이트로 나가는 링크는 누르지 않는다. 동작 없는 대상이 있으면 종료 코드 1로 끝난다."),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			address := args[0]
			if !strings.Contains(address, "://") {
				abs, err := filepath.Abs(address)
				if err != nil {
					return err
				}
				address = "file://" + filepath.ToSlash(abs)
			}
			browser := uicheck.FindBrowser()
			if browser == "" {
				return errors.New(lang.T("Chrome 계열 브라우저를 찾지 못했다. SAMCHEONPO_BROWSER로 경로를 지정한다"))
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			r, err := uicheck.Check(ctx, browser, address)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if format == "json" {
				enc := json.NewEncoder(w)
				enc.SetIndent("", "  ")
				if err := enc.Encode(r); err != nil {
					return err
				}
			} else {
				counts := map[string]int{}
				for _, t := range r.Targets {
					counts[t.Effect]++
				}
				fmt.Fprintf(w, lang.T("클릭 대상 %d개: 동작 있음 %d, 동작 없음 %d, 건너뜀 %d, 눌러 보지 못함 %d\n"), len(r.Targets), counts[uicheck.EffectChanged]+counts[uicheck.EffectNavigate]+counts[uicheck.EffectLink], counts[uicheck.EffectNone], counts[uicheck.EffectSkipped], counts[uicheck.EffectError])
				for _, t := range r.Dead() {
					fmt.Fprintf(w, lang.T("동작 없음: %q (%s)\n"), t.Label, t.Tag)
				}
				for _, l := range r.Repeated {
					fmt.Fprintf(w, lang.T("같은 문구와 목적지로 3번 이상 반복: %q\n"), l)
				}
				if r.OutOfOrder > 0 {
					fmt.Fprintf(w, lang.T("문서 순서와 화면 순서가 어긋난 대상 %d개\n"), r.OutOfOrder)
				}
				fmt.Fprintln(w, lang.T("화면에서 읽는 순서:"))
				for _, o := range r.Order {
					fmt.Fprintln(w, "  "+o)
				}
			}
			if len(r.Dead()) > 0 {
				return fmt.Errorf(lang.T("동작 없는 클릭 대상 %d개"), len(r.Dead()))
			}
			return nil
		},
	}
	c.Flags().StringVar(&format, "format", "text", "text|json")
	c.Flags().DurationVar(&timeout, "timeout", 2*time.Minute, lang.T("전체 제한 시간"))
	return c
}

func restraintEvalCmd() *cobra.Command {
	return &cobra.Command{
		Use:   lang.T("restraint-eval <사례 파일>"),
		Short: lang.T("요청마다 정한 최소 충분 답보다 넘친 문단과 요소를 센다"),
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cases, err := eval.LoadRestraint(args[0])
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			excess, missing := 0, 0
			for _, c := range cases {
				r := eval.ScoreRestraint(c)
				excess += len(r.Excess)
				missing += len(r.Missing)
				fmt.Fprintf(w, lang.T("%-34s 넘침 %d  누락 %d\n"), r.Name, len(r.Excess), len(r.Missing))
			}
			fmt.Fprintf(w, lang.T("사례 %d개: 넘친 항목 %d개, 빠진 필수 항목 %d개\n"), len(cases), excess, missing)
			return nil
		},
	}
}
