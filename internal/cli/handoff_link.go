package cli

import (
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/adapter"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"github.com/spf13/cobra"
)

func handoffLinkCmd() *cobra.Command {
	var from, to, session string
	var includesChildren bool
	c := &cobra.Command{
		Use: "link <parent-session>", Short: "새 세션을 기존 작업에 명시적으로 연결 (실행 승인 아님)", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, ok := adapter.Profiles[from]; !ok {
				return fmt.Errorf("알 수 없는 원본 에이전트")
			}
			if _, ok := adapter.Profiles[to]; !ok || session == "" {
				return fmt.Errorf("새 에이전트와 세션 ID가 필요하다")
			}
			db, err := openDB()
			if err != nil {
				return err
			}
			defer db.Close()
			task, _, _, err := db.SessionIntent(from, args[0])
			if err != nil {
				return err
			}
			link := intent.SessionLink{TaskID: task.ID, Agent: to, SessionID: session, ParentAgent: from, ParentSessionID: args[0], IncludesChildren: includesChildren}
			if err := db.LinkTaskSession(link); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "작업 %s: %s/%s → %s/%s 연결을 기록했습니다. 새 세션 시작/재시작에서 적용되며 현재 실행 중인 세션의 목표·승인·권한은 바꾸지 않습니다.\n", task.ID, from, args[0], to, session)
			return nil
		},
	}
	c.Flags().StringVar(&from, "from", "claude", "부모 에이전트")
	c.Flags().StringVar(&to, "agent", "codex", "자식 에이전트")
	c.Flags().StringVar(&session, "session", "", "새 세션 ID; 기존 다른 작업의 ID를 재지정할 수 없음")
	c.Flags().BoolVar(&includesChildren, "includes-children", false, "이 새 세션의 사용량이 자손 사용량을 포함한다는 명시적 선언")
	return c
}
