package cli

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/assessment"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/spf13/cobra"
)

func diagnosticCmd(name string) *cobra.Command {
	var format string
	c := &cobra.Command{Use: name + " <session>", Short: "판정·비용 귀속·판단 불가 사유와 관측 범위를 읽기 전용으로 설명", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		db, err := ledger.OpenReadOnly(dbPath())
		if err != nil {
			return err
		}
		defer db.Close()
		id, _, _, err := db.SourceOf(args[0])
		if err != nil {
			return err
		}
		evs, err := db.EventsOf(id)
		if err != nil {
			return err
		}
		vs, err := db.VerdictsOf(id)
		if err != nil {
			return err
		}
		channels, err := db.DeliveryChannels(id)
		if err != nil {
			return err
		}
		s := assessment.BuildRecorded(evs, vs, channels)
		var contractHash string
		if err = db.QueryRow(`SELECT COALESCE(contract_hash,'') FROM session WHERE id=?`, id).Scan(&contractHash); err != nil {
			return err
		}
		if contractHash == "" {
			s.Reasons["contract_unaccepted"]++
		}
		// The stored analysis carries what the observation could not cover.
		// Reporting it here keeps "no finding" from reading as "nothing was
		// wrong" when the monitor could not see the workspace.
		if obs, err := db.ObservationOf(id); err == nil {
			switch obs.WatchState {
			case "unavailable":
				s.Reasons["watch_unavailable"]++
			case "degraded":
				s.Reasons["watch_degraded"]++
			}
			switch obs.CandidatePack {
			case "unreadable":
				s.Reasons["learned_rules_unreadable"]++
			case "invalid":
				s.Reasons["learned_rules_invalid"]++
			}
		}
		if format == "json" {
			b, err := json.MarshalIndent(s, "", "  ")
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), string(b))
			return err
		}
		if format != "text" {
			return fmt.Errorf("format은 text 또는 json")
		}
		w := cmd.OutOrStdout()
		fmt.Fprintln(w, s.Text())
		fmt.Fprintf(w, "관측된 전경 셸 %d건 중 종료 결과 %d건 · 작업 폴더 지문 %d건 · 결과 지문 %d건\n전체 수집률 미확인 · 완료 여부 미확인\n", s.Coverage.ShellCalls, s.Coverage.CompletedShellResults, s.Coverage.WorkspaceKnown, s.Coverage.ResultKnown)
		for _, key := range s.ReasonKeys() {
			fmt.Fprintf(w, "  %s: %d\n", reasonText(key), s.Reasons[key])
		}
		fmt.Fprintln(w, "탐지 없음은 정상이나 완료의 증거가 아닙니다. 판정 당시 비용과 뒤늦게 계측된 비용은 다를 수 있습니다.")
		return nil
	}}
	c.Flags().StringVar(&format, "format", "text", "text | json")
	return c
}

func reasonText(code string) string {
	if text := map[string]string{"missing_exit": "종료 결과 없음", "missing_ws": "작업 폴더 지문 없음", "missing_result": "결과 지문 없음", "contract_unaccepted": "기록된 수락 계약 없음", "completion_unobserved": "현재 완료 근거 없음", "usage_unobserved": "사용량 미관측", "execution_context_unknown": "실행 환경·효과 해석 불가", "environment_unknown": "환경 동일성 미확인", "new_evidence": "새 근거·환경 변경", "shadow": "기록 전용 규칙", "cooldown": "반복 안내 억제", "background_pending": "백그라운드 실행 결과 대기", "rollout_unrecorded": "당시 전달 방식 기록 없음", "semantic_unknown": "의미 판정 근거 부족", "semantic_deferred": "의미 판정 호출 보류", "unsupported_route": "지원하는 전달 경로 없음", "candidate_limit": "후보 보관 상한", "flaky": "불안정한 검사", "watch_unavailable": "파일 감시 없음", "watch_degraded": "파일 감시 일부", "learned_rules_unreadable": "학습 규칙 읽지 못함", "learned_rules_invalid": "학습 규칙 형식 오류"}[code]; text != "" {
		return text
	}
	return code
}

func ledgerGaps(since time.Time, agent string) ([]analyze.Gap, error) {
	db, err := ledger.OpenReadOnly(dbPath())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if agent == "" {
		agent = "all"
	}
	rows, err := db.Sessions(since, agent, "")
	if err != nil {
		return nil, err
	}
	var out []analyze.Gap
	for _, row := range rows {
		evs, err := db.EventsOf(row.ID)
		if err != nil {
			return nil, err
		}
		s := &event.Session{ID: row.ID, Agent: row.Agent, Events: evs}
		out = append(out, analyze.Gaps(&analyze.Result{Session: s})...)
	}
	return out, nil
}

func taskCoverageCmd() *cobra.Command {
	return &cobra.Command{Use: "task-coverage <session>", Short: "명시 연결된 작업의 중복 제외·세션 간 반복 후보를 조회", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		db, err := ledger.OpenReadOnly(dbPath())
		if err != nil {
			return err
		}
		defer db.Close()
		id, _, agent, err := db.SourceOf(args[0])
		if err != nil {
			return err
		}
		task, _, _, err := db.SessionIntent(agent, id)
		if err != nil {
			return err
		}
		links, err := db.TaskSessions(task.ID)
		if err != nil {
			return err
		}
		var nodes []assessment.TaskNode
		for _, link := range links {
			evs, err := db.EventsOf(link.SessionID)
			if err != nil {
				return err
			}
			nodes = append(nodes, assessment.TaskNode{Link: link, Events: evs})
		}
		result, err := assessment.LinkedTask(nodes)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	}}
}
