package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/learning"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rulepack"
	"github.com/spf13/cobra"
	"io"
	"os"
	"path/filepath"
)

func learningInputs(labelsFile, splitFile string, holdout bool) (eval.Split, []eval.Label, map[string][]*event.Event, error) {
	split, err := eval.LoadSplit(splitFile)
	if err != nil {
		return split, nil, nil, err
	}
	labels, err := eval.LoadLabels(labelsFile)
	if err != nil {
		return split, nil, nil, err
	}
	db, err := ledger.OpenReadOnly(dbPath())
	if err != nil {
		return split, nil, nil, err
	}
	defer db.Close()
	ids := append([]string(nil), split.Calibration...)
	if holdout {
		ids = append(ids, split.Holdout...)
	}
	data := map[string][]*event.Event{}
	for _, id := range ids {
		var mode, closed string
		if err = db.QueryRow(`SELECT s.mode,COALESCE(o.close_reason,'') FROM session s LEFT JOIN session_observation o ON o.session_id=s.id WHERE s.id=?`, id).Scan(&mode, &closed); err != nil {
			return split, nil, nil, err
		}
		if mode != "audit" && (closed == "" || closed == ledger.ObservationOpen) {
			return split, nil, nil, errors.New("learning requires closed or audited sessions")
		}
		evs, err := db.EventsOf(id)
		if err != nil {
			return split, nil, nil, err
		}
		if len(evs) == 0 {
			return split, nil, nil, errors.New("empty learning source")
		}
		data[id] = evs
	}
	return split, labels, data, nil
}
func loadProposal(path string) (learning.Proposal, error) {
	var p learning.Proposal
	b, err := os.ReadFile(path)
	if err != nil {
		return p, err
	}
	if len(b) > 1<<20 {
		return p, errors.New("proposal too large")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err = d.Decode(&p); err != nil {
		return p, err
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return p, errors.New("trailing proposal data")
	}
	return p, p.Validate()
}
func writeLearning(cmd *cobra.Command, out string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	if out != "" {
		return writeAtomicMode(out, b, 0600)
	}
	_, err = cmd.OutOrStdout().Write(b)
	return err
}

// evidenceTierLabel names the grade of evidence behind a rule pack.
//
// A pack built on machine-derived evidence is a weaker claim than one a
// person confirmed, and the person who installs it has to be able to tell
// the two apart.
func evidenceTierLabel(tier string) string {
	switch tier {
	case rulepack.EvidenceDerivedExecution:
		return "실행 기반 파생"
	default:
		return "사람 라벨"
	}
}

func learningCmds() []*cobra.Command {
	var labels, split, out string
	var precision, fpr float64
	propose := &cobra.Command{Use: "propose", Short: "사람 신고의 학습 구간에서 고정된 비차단 반복 규칙을 설계", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		s, ls, data, err := learningInputs(labels, split, false)
		if err != nil {
			return err
		}
		p, err := learning.ProposeWithGate(ls, s, data, learning.Gate{PrecisionLower: precision, FPRUpper: fpr})
		if err != nil {
			return err
		}
		return writeLearning(cmd, out, p)
	}}
	propose.Flags().StringVar(&labels, "labels", "", "사람 라벨 디렉터리")
	propose.Flags().StringVar(&split, "split", "", "사전 격리 split/2")
	propose.Flags().StringVar(&out, "out", "", "고정 제안 파일")
	propose.Flags().Float64Var(&precision, "precision-min", .9, "사전 지정 precision 신뢰구간 하한")
	propose.Flags().Float64Var(&fpr, "fpr-max", .05, "사전 지정 오탐률 신뢰구간 상한")
	var evLabels, evSplit, evOut string
	var check bool
	evaluate := &cobra.Command{Use: "evaluate <proposal.json>", Short: "고정 규칙을 독립 holdout 사람 라벨로 평가", Args: cobra.ExactArgs(1), SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		p, err := loadProposal(args[0])
		if err != nil {
			return err
		}
		s, ls, data, err := learningInputs(evLabels, evSplit, true)
		if err != nil {
			return err
		}
		r, err := learning.Evaluate(p, s, ls, data, p.Gate)
		if err != nil {
			return err
		}
		if err = writeLearning(cmd, evOut, r); err != nil {
			return err
		}
		if check && !r.Passed {
			return errors.New("독립 평가 근거 부족 또는 규칙 기준 미충족")
		}
		return nil
	}}
	evaluate.Flags().StringVar(&evLabels, "labels", "", "독립 라벨 디렉터리")
	evaluate.Flags().StringVar(&evSplit, "split", "", "고정 split/2")
	evaluate.Flags().StringVar(&evOut, "out", "", "평가 보고서")
	evaluate.Flags().BoolVar(&check, "check", false, "보류·미충족이면 실패 종료")
	var promoteLabels, promoteSplit, promoteOut string
	var install bool
	promote := &cobra.Command{Use: "promote <proposal.json>", Short: "독립 평가를 재계산한 뒤 규칙 팩 생성·선택 설치 (기본 기록 전용)", Args: cobra.ExactArgs(1), SilenceUsage: true, RunE: func(cmd *cobra.Command, args []string) error {
		p, err := loadProposal(args[0])
		if err != nil {
			return err
		}
		s, ls, data, err := learningInputs(promoteLabels, promoteSplit, true)
		if err != nil {
			return err
		}
		r, err := learning.Evaluate(p, s, ls, data, p.Gate)
		if err != nil {
			return err
		}
		pack, err := learning.Promote(p, r)
		if err != nil {
			return err
		}
		if install {
			if err = os.MkdirAll(config.Home(), 0700); err != nil {
				return err
			}
			if err = writeLearning(cmd, filepath.Join(config.Home(), "learned-rules.json"), pack); err != nil {
				return err
			}
			for _, d := range pack.Definitions {
				fmt.Fprintf(cmd.OutOrStdout(), "기록 전용 설치: %s · %s 근거 · 안내 선택은 사용자 recommend_rules에 명시\n", d.RuleID(), evidenceTierLabel(pack.EvidenceTier))
			}
		}
		if promoteOut != "" || !install {
			return writeLearning(cmd, promoteOut, pack)
		}
		return nil
	}}
	promote.Flags().StringVar(&promoteLabels, "labels", "", "독립 라벨 디렉터리")
	promote.Flags().StringVar(&promoteSplit, "split", "", "고정 split/2")
	promote.Flags().StringVar(&promoteOut, "out", "", "평가 통과 팩 파일")
	promote.Flags().BoolVar(&install, "install", false, "사용자 전용 learned-rules.json에 설치; 설정·데몬은 바꾸지 않음")
	return []*cobra.Command{propose, evaluate, promote}
}
