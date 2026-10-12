package cli

import (
	"errors"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/analyze"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/config"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rulepack"
	"github.com/spf13/cobra"
	"os"
)

func compareCmd() *cobra.Command {
	var queuePath, labelsDir, format string
	var editThreshold int
	c := &cobra.Command{Use: "compare", Short: "고정된 실제 검토 구간에 참조 휴리스틱을 비교 (SDK 실행·효과 검증 아님)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if editThreshold < 2 || editThreshold > 1000 {
			return errors.New("edit threshold must be 2..1000")
		}
		q, err := eval.LoadReviewQueue(queuePath)
		if err != nil {
			return err
		}
		db, err := ledger.OpenReadOnly(dbPath())
		if err != nil {
			return err
		}
		defer db.Close()
		var labels []eval.Label
		prices, err := cost.Load("")
		if err != nil {
			return err
		}
		cfg := config.Default()
		scratch, err := os.MkdirTemp("", "samcheonpo-current-replay-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(scratch)
		if labelsDir != "" {
			labels, err = eval.LoadLabels(labelsDir)
			if err != nil {
				return err
			}
		}
		type row struct {
			SessionHash         string                     `json:"session_hash"`
			Consensus           string                     `json:"review_consensus"`
			Reason              string                     `json:"review_reason"`
			Predictions         []eval.ReferencePrediction `json:"references"`
			RecordedFindings    int                        `json:"recorded_findings"`
			ReferenceSource     string                     `json:"reference_source"`
			CurrentReplayRules  map[string]int             `json:"current_replay_rules"`
			CurrentReplayStatus string                     `json:"current_replay_status"`
			PredictionStates    map[string]string          `json:"prediction_states"`
		}
		rows := []row{}
		cases := []eval.ComparisonCase{}
		detectors := []string{"current_rules", "identical_action_result", "per_file_edit_count"}
		for _, item := range q.Items {
			events, err := db.EventsOf(item.Session)
			if err != nil {
				return err
			}
			if eval.ReviewSourceDigest(events) != item.SourceDigest {
				return errors.New("frozen review source changed")
			}
			// Preserve preceding events for reference windows, then count only
			// predictions whose trigger is in the independently reviewed interval.
			referenceEvents, sourceState := nativeReferenceEvents(db, item, events)
			// Later turns cannot change an earlier interval's prediction or
			// observation eligibility. Retain only its preceding context.
			prefix := referenceEvents[:0:0]
			for _, ev := range referenceEvents {
				if ev.Seq <= item.End {
					prefix = append(prefix, ev)
				}
			}
			referenceEvents = prefix
			predictions := eval.ReferencePredictions(referenceEvents, editThreshold)
			for i := range predictions {
				filtered := [][]int64{}
				for _, seqs := range predictions[i].Evidence {
					trigger := seqs[len(seqs)-1]
					if trigger >= item.Start && trigger <= item.End {
						filtered = append(filtered, seqs)
					}
				}
				predictions[i].Evidence = filtered
			}
			vs, err := db.VerdictsOf(item.Session)
			if err != nil {
				return err
			}
			count := 0
			for _, v := range vs {
				if v.Seq >= item.Start && v.Seq <= item.End {
					count++
				}
			}
			consensus, reason := eval.FrozenReviewConsensus(item, q.Digest, labels)
			currentRules := map[string]int{}
			currentStatus := "native_source_unverified"
			states := map[string]string{"current_rules": "unknown"}
			for _, p := range predictions {
				states[p.Detector] = "unknown"
				if sourceState == "native_frozen_bytes_verified" {
					if len(p.Evidence) > 0 {
						states[p.Detector] = "positive"
					} else if p.Unknown == 0 {
						states[p.Detector] = "negative"
					}
				}
			}
			if sourceState == "native_frozen_bytes_verified" {
				var goal string
				for _, e := range referenceEvents {
					if e.Kind == event.KindPrompt && e.PromptOrigin != "tool" && e.Text != "" {
						goal = e.Text
						break
					}
				}
				s := &event.Session{ID: item.Session, Agent: item.Agent, Mode: "audit", FirstPrompt: goal, ProjectPath: scratch, Events: referenceEvents}
				if replay, e := analyze.Run(s, analyze.Options{Config: cfg, Prices: prices}); e == nil && replay.FormatOK {
					currentStatus = "offline_replay_not_delivered"
					for _, v := range replay.Verdicts {
						if v.Seq >= item.Start && v.Seq <= item.End {
							currentRules[v.Rule]++
						}
					}
					states["current_rules"] = "negative"
					for _, e := range referenceEvents {
						if e.Kind == event.KindTool && (!e.ResultObserved || e.Unknown || e.Background) {
							states["current_rules"] = "unknown"
						}
						for _, d := range e.Decisions {
							if d.Code != "shadow" && d.Code != "cooldown" {
								states["current_rules"] = "unknown"
							}
						}
					}
					if len(currentRules) > 0 {
						states["current_rules"] = "positive"
					}
				} else {
					currentStatus = "offline_replay_failed"
				}
			}
			rows = append(rows, row{rulepack.Digest(item.Session), consensus, reason, predictions, count, sourceState, currentRules, currentStatus, states})
			cases = append(cases, eval.ComparisonCase{Consensus: consensus, Reason: reason, Predictions: states})
		}
		report := map[string]any{"version": "reference-comparison/3", "descriptive_scores": eval.ScoreComparison(cases, detectors), "replay_evaluator_version": analyze.EvaluatorVersion, "replay_config_hash": config.Hash(cfg), "replay_price_version": prices.Version, "queue_digest": q.Digest, "review_intervals": len(rows), "label_records": len(labels), "effectiveness_validated": false, "independent_humans_verified": false, "limitations": "실제 원장의 고정 구간에 대한 정규화 휴리스틱 비교. 외부 SDK는 실행하지 않음. 구형 원장의 원본 호출/결과 지문 부재는 unknown. 원장 후보를 층화 표집했으므로 모집단 정밀도·재현율로 일반화할 수 없음. 지표는 모든 비교기가 판정 가능하고 검토 답변이 합의된 동일 구간에서만 계산하며, 분모가 없으면 null이다. 구간 이전의 미관측·보류 근거도 보수적으로 유보한다. 사람 검토자 신원·독립성 확인 및 실사용 on/off 효과는 별도 인수.", "runs": rows}
		return printJSONOr(cmd.OutOrStdout(), format, report, "참조 비교 실행됨. 수치·유보 사유는 --format json으로 확인. 실제 효과 검증은 미완료.\n")
	}}
	c.Flags().StringVar(&queuePath, "queue", "", "고정된 실제 검토 큐 JSON")
	c.Flags().StringVar(&labelsDir, "labels", "", "실제 사람 라벨 폴더; 생략 시 정답 미확인")
	c.Flags().IntVar(&editThreshold, "edit-threshold", 10, "작성한 per-file 편집 휴리스틱의 명시 임계값; 공식 SDK 기본값 아님")
	c.Flags().StringVar(&format, "format", "json", "json | text")
	return c
}
