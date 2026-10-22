package cli

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/patch"
)

// forkCmd returns the fork command.
//
// A fork returns the workspace to a recorded passing point and starts a new
// session from there. The failed session is kept and marked as diverged, so
// a receipt can tell a branch from a session that simply ended.
//
// The command previews by default. Restoring files and opening a new
// session is an explicit act.
func forkCmd() *cobra.Command {
	var session, golden string
	var apply bool
	c := &cobra.Command{
		Use:   "fork",
		Short: "기록된 통과 시점으로 되돌리고 새 세션을 연다",
		Long: "fork는 워크스페이스를 기록된 통과 시점으로 되돌리고 그 자리에서 새 세션을 시작한다.\n" +
			"실패한 세션은 지우지 않는다. 분기 상태로 남아 영수증에서 분기임을 알 수 있다.\n" +
			"기본은 미리보기이며, 되돌리려면 --apply를 붙인다.",
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openDB()
			if err != nil {
				return err
			}
			defer db.Close()
			w := cmd.OutOrStdout()
			srcID := session
			if srcID == "" {
				srcID, _, _, err = db.LatestLiveSession()
				if err != nil {
					return err
				}
			}
			_, root, err := db.SessionRoot(srcID)
			if err != nil {
				return err
			}
			g := patch.NewGoldenStateManager(root)
			snapID := golden
			if snapID == "" {
				snapID = g.LatestSnapshotID()
			}
			if snapID == "" {
				return fmt.Errorf("기록된 통과 시점이 없다. 검사가 통과하면 그 시점이 기록된다")
			}
			meta, err := g.Get(snapID)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "기준 세션: %s\n", srcID)
			fmt.Fprintf(w, "통과 시점: %s  (%s, 이벤트 %d, 파일 %d개)\n", meta.ID, meta.Reason, meta.Seq, len(meta.Files))
			if !apply {
				fmt.Fprintf(w, "\n되돌릴 파일:\n")
				for _, f := range meta.Files {
					fmt.Fprintf(w, "  %s\n", f)
				}
				fmt.Fprintf(w, "\n실행하려면 --apply를 붙인다: samcheonpo fork --golden %s --apply\n", meta.ID)
				return nil
			}
			restored, skipped, err := g.Restore(meta.ID)
			if err != nil {
				return err
			}
			newID := srcID + "-fork-" + time.Now().UTC().Format("20060102-1504")
			note := fmt.Sprintf("이 세션은 %s에서 분기했다. 기록된 통과 시점 %s(%s)로 되돌렸다. "+
				"이전 가설은 이미 기각됐으므로 같은 접근을 반복하지 말고 다른 전략을 세워라.",
				srcID, meta.ID, meta.Reason)
			if _, err := db.BranchSession(srcID, newID, note); err != nil {
				return err
			}
			fmt.Fprintf(w, "\n되돌린 파일 %d개:\n", len(restored))
			for _, f := range restored {
				fmt.Fprintf(w, "  %s\n", f)
			}
			if len(skipped) > 0 {
				fmt.Fprintf(w, "\n건너뛴 파일 %d개 (프로젝트 밖이라 복구하지 않음):\n", len(skipped))
				for _, f := range skipped {
					fmt.Fprintf(w, "  %s\n", f)
				}
			}
			fmt.Fprintf(w, "\n새 세션: %s\n", newID)
			fmt.Fprintf(w, "%s\n", note)
			return nil
		},
	}
	c.Flags().StringVar(&session, "session", "", "분기할 기준 세션 ID (기본: 이 프로젝트의 가장 최근 진행 세션)")
	c.Flags().StringVar(&golden, "golden", "", "되돌릴 통과 시점 ID (기본: 가장 최근 통과 시점)")
	c.Flags().BoolVar(&apply, "apply", false, "실제로 되돌리고 새 세션을 연다")
	return c
}
