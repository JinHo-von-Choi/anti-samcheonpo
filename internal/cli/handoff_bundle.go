package cli

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/handoff"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/live"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/verification"
	"github.com/spf13/cobra"
)

func buildHandoff(db *ledger.DB, agent, session, root string) (handoff.Bundle, error) {
	b := handoff.Bundle{Version: handoff.Version, CreatedAt: time.Now().UTC(), Source: handoff.SessionRef{Agent: agent, ID: session}}
	var err error
	b.Recoveries, err = db.Recoveries(agent, session)
	if err != nil {
		return b, err
	}
	b.ObservationGap, err = db.ObservationGap(agent, session)
	if err != nil {
		return b, err
	}
	b.Failures, err = db.FailedApproaches(agent, session)
	if err != nil {
		return b, err
	}
	if len(b.Failures) == 20 {
		b.Unknown = append(b.Unknown, "실패 실행은 최신 20건으로 제한한 요약이며 전체 이력은 원장에서 확인해야 함")
	}
	task, r, _, err := db.SessionIntent(agent, session)
	if errors.Is(err, sql.ErrNoRows) {
		b.Unknown = append(b.Unknown, "이전 세션에 작업·의도 개정 원장이 없어 목표 연속성과 비용 총합을 확정할 수 없음")
		return b, b.Validate()
	}
	if err != nil {
		return b, err
	}
	b.Task = &task
	b.Revisions, err = db.TaskRevisions(task.ID)
	if err != nil {
		return b, err
	}
	if len(b.Revisions) == 0 || b.Revisions[len(b.Revisions)-1].Number != r.Number {
		return b, errors.New("인수인계 조회 중 목표가 바뀌었다. 최신 상태로 다시 요청해야 한다")
	}
	b.Evidence, err = db.TaskEvidence(task.ID, r.Number)
	if err != nil {
		return b, err
	}
	b.Sessions, err = db.TaskSessions(task.ID)
	if err != nil {
		return b, err
	}
	for _, link := range b.Sessions {
		if link.Agent != agent || link.SessionID != session {
			gap, err := db.ObservationGap(link.Agent, link.SessionID)
			if err != nil {
				return b, err
			}
			if gap > ^uint64(0)-b.ObservationGap {
				return b, errors.New("관측 누락 수 범위 초과")
			}
			b.ObservationGap += gap
		}
		u, err := db.HandoffUsage(link.Agent, link.SessionID)
		if err != nil {
			return b, err
		}
		b.Usage = append(b.Usage, u)
	}
	if task.ProjectID == fp.Hash("project", filepath.Clean(root)) {
		c, _, err := contract.Load(root)
		if err == nil && c != nil && c.ChecksHash() == r.ContractHash && c.Goal == r.Goal {
			b.Contract = c
		}
	}
	if b.Contract == nil {
		b.Unknown = append(b.Unknown, "최신 의도 개정과 일치하는 계약을 현재 경로에서 확인하지 못함")
	}
	b.Unknown = append(b.Unknown, "수신 환경의 입력·환경 지문과 실행 승인은 별도 확인 필요", "세션 간 사용량 포함 관계를 확인하기 전 총합 미확정")
	return b, b.Validate()
}

func handoffInspectCmd() *cobra.Command {
	var verifyInputs bool
	c := &cobra.Command{
		Use: "inspect <bundle.json>", Short: "인수인계 원문을 실행·승인 없이 읽는다", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			b, err := handoff.Decode(f)
			if err != nil {
				return err
			}
			var keys map[string]verification.Key
			if verifyInputs {
				root, err := os.Getwd()
				if err != nil {
					return err
				}
				keys, err = receiverKeys(b, root)
				if err != nil {
					return err
				}
			}
			fmt.Fprintln(cmd.OutOrStdout(), b.Text(keys, time.Now()))
			return nil
		},
	}
	c.Flags().BoolVar(&verifyInputs, "verify-inputs", false, "현재 수락된 계약과 로컬 입력·환경 지문만 확인; 검사 명령은 실행하지 않음")
	return c
}

func receiverKeys(b handoff.Bundle, root string) (map[string]verification.Key, error) {
	if b.Task == nil || b.Contract == nil || len(b.Revisions) == 0 {
		return nil, errors.New("연결된 작업·계약이 없는 묶음")
	}
	c, raw, err := contract.Load(root)
	if err != nil {
		return nil, err
	}
	if c == nil || c.Goal != b.Contract.Goal || c.ChecksHash() != b.Contract.ChecksHash() || contract.CurrentState(root, c, raw).State != contract.StateAccepted {
		return nil, errors.New("수신 환경에서 같은 계약을 별도로 수락해야 지문을 확인할 수 있다")
	}
	ws := live.NewWorkspace(root)
	defer ws.Close()
	runner := live.Runner{Root: root, WS: ws}
	runner.SetAuthority(contract.AuthorityDigest(c))
	keys := map[string]verification.Key{}
	for _, check := range c.MachineChecks() {
		key, err := runner.CurrentKey(check, b.Task.ID, b.Revisions[len(b.Revisions)-1].Number)
		if err == nil {
			keys[check.ID] = key
		}
	}
	// A matching fingerprint cannot authenticate a claimed historical pass.
	// Only exact evidence already present in this receiver's local ledger is
	// eligible for a skip recommendation; portable JSON alone stays historical.
	if len(b.Evidence) > 0 {
		if _, err := os.Stat(dbPath()); err != nil {
			return map[string]verification.Key{}, nil
		}
		db, err := openDB()
		if err != nil {
			return nil, err
		}
		defer db.Close()
		latest, err := db.LatestIntent(b.Task.ID)
		if err != nil || latest.Number != b.Revisions[len(b.Revisions)-1].Number {
			return map[string]verification.Key{}, nil
		}
		links, err := db.TaskSessions(b.Task.ID)
		if err != nil {
			return nil, err
		}
		for _, link := range links {
			gap, err := db.ObservationGap(link.Agent, link.SessionID)
			if err != nil {
				return nil, err
			}
			if gap > 0 {
				return map[string]verification.Key{}, nil
			}
		}
		trusted := map[string]bool{}
		for _, incoming := range b.Evidence {
			stored, err := db.Evidence(incoming.Key)
			if err != nil {
				return nil, err
			}
			found := false
			for _, local := range stored {
				if reflect.DeepEqual(local, incoming) {
					found = true
				}
			}
			previous, seen := trusted[incoming.Key.CheckID]
			trusted[incoming.Key.CheckID] = found && (!seen || previous)
		}
		for check := range keys {
			if !trusted[check] {
				delete(keys, check)
			}
		}
	}
	return keys, nil
}
