package cli

import (
	"encoding/json"
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/rulepack"
	"github.com/spf13/cobra"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type ReviewItem = eval.ReviewItem
type ReviewQueue = eval.ReviewQueue

func reviewQueueCmd() *cobra.Command {
	var out, since string
	var candidates, controls int
	c := &cobra.Command{Use: "prepare-review", Short: "실제 원장에서 독립 사람 검토 큐를 고정 (정답 라벨 생성 없음)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		if out == "" || candidates < 1 || controls < 1 || candidates > 10000 || controls > 10000 {
			return fmt.Errorf("--out과 1~10000 범위의 표본 목표가 필요합니다")
		}
		from, err := ParseSince(since, time.Now())
		if err != nil {
			return err
		}
		db, err := ledger.OpenReadOnly(dbPath())
		if err != nil {
			return err
		}
		defer db.Close()
		queue, err := prepareQueue(db, from, candidates, controls)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(out, 0700); err != nil {
			return err
		}
		raw, err := json.MarshalIndent(queue, "", "  ")
		if err != nil {
			return err
		}
		if err = writeAtomicMode(filepath.Join(out, "review-queue.json"), append(raw, '\n'), 0600); err != nil {
			return err
		}
		// A public summary excludes raw session ids and paths.
		summary := map[string]any{"version": queue.Version, "queue_digest": queue.Digest, "candidate_intervals": queue.Candidates, "unreviewed_control_intervals": queue.Controls, "platforms": queue.Platforms, "labels_created": false, "promotion_eligible": false, "limitations": queue.Limitation}
		raw, err = json.MarshalIndent(summary, "", "  ")
		if err != nil {
			return err
		}
		if err = writeAtomicMode(filepath.Join(out, "summary.json"), append(raw, '\n'), 0600); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "사람 검토 큐: 후보 %d/%d · 미검토 대조 %d/%d · 플랫폼 %d종\n정답 라벨 0건 · 승격 불가 · 별도 두 사람 독립 검토 필요\n", queue.Candidates, candidates, queue.Controls, controls, len(queue.Platforms))
		return nil
	}}
	c.Flags().StringVar(&out, "out", "", "접근 제한 검토 자료 폴더")
	c.Flags().StringVar(&since, "since", "30d", "자료 조회 기간")
	c.Flags().IntVar(&candidates, "candidate-target", 200, "의심/실패 후보 목표; 정답 양성 수가 아님")
	c.Flags().IntVar(&controls, "control-target", 400, "정상 여부를 사람이 검토할 대조 구간 목표")
	return c
}
func prepareQueue(db *ledger.DB, from time.Time, candidateTarget, controlTarget int) (ReviewQueue, error) {
	q := ReviewQueue{Version: "review-queue/2", CreatedAt: time.Now().UTC(), RequestedCandidates: candidateTarget, RequestedControls: controlTarget, Platforms: map[string]int{}, Items: []ReviewItem{}, Limitation: "후보와 대조를 추출한 미검토 자료. 탐지 없음은 정상 라벨이 아니다. 두 사람의 독립 라벨·불일치 조정·저장소/실패 계열 격리·원본 변형 중복 확인 전 precision/recall과 승격은 미확인. 구조화 근거만으로 의미를 판단할 수 없는 구간은 unsure로 남긴다."}
	rows, err := db.Sessions(from, "all", "")
	if err != nil {
		return q, err
	}
	sort.Slice(rows, func(i, j int) bool { return rulepack.Digest(rows[i].ID) < rulepack.Digest(rows[j].ID) })
	seen := map[string]bool{}
	seenNative := map[string]bool{}
	type sourceStamp struct {
		size     int64
		modified time.Time
		digest   string
	}
	sourceCache := map[string]sourceStamp{}
	for _, row := range rows {
		if q.Candidates >= candidateTarget && q.Controls >= controlTarget {
			break
		}
		// Freeze only stable closed or audit sources.
		var state string
		if err := db.QueryRow(`SELECT COALESCE(close_reason,'') FROM session_observation WHERE session_id=?`, row.ID).Scan(&state); err != nil && row.Mode != "audit" {
			continue
		}
		if row.Mode != "audit" && (state == "" || state == ledger.ObservationOpen) {
			continue
		}
		events, err := db.EventsOf(row.ID)
		if err != nil {
			return q, err
		}
		if len(events) < 3 {
			continue
		}
		digest := eval.ReviewSourceDigest(events)
		// Audit/live duplicates with the same recorded source hash stay one source.
		var original string
		if err := db.QueryRow(`SELECT COALESCE(source_hash,'') FROM session WHERE id=?`, row.ID).Scan(&original); err != nil {
			return q, err
		}
		key := digest
		if original != "" {
			key = original
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		vs, err := db.VerdictsOf(row.ID)
		if err != nil {
			return q, err
		}
		strata := "unreviewed_control"
		start, end := events[0].Seq, events[min(len(events)-1, 63)].Seq
		if len(vs) > 0 {
			strata = "candidate"
			start = vs[0].Seq
			end = start
			if len(vs[0].Evidence) > 0 {
				start = vs[0].Evidence[0]
				end = start
				for _, seq := range vs[0].Evidence {
					start = min(start, seq)
					end = max(end, seq)
				}
			}
		}
		if strata == "candidate" && q.Candidates >= candidateTarget || strata == "unreviewed_control" && q.Controls >= controlTarget {
			continue
		}
		hasStart, hasEnd := false, false
		applied := false
		for _, e := range events {
			hasStart = hasStart || e.Seq == start
			hasEnd = hasEnd || e.Seq == end
			applied = applied || e.ResultObserved && e.Category == event.CatProduce
		}
		if !hasStart || !hasEnd {
			continue
		}
		available := false
		nativeDigest := ""
		if row.Source != "" {
			if st, err := os.Stat(row.Source); err == nil && st.Mode().IsRegular() {
				available = true
				if cached, ok := sourceCache[row.Source]; ok && cached.size == st.Size() && cached.modified.Equal(st.ModTime()) {
					nativeDigest = cached.digest
				} else if st.Size() <= 32<<20 {
					if sha, err := eval.NativeSourceDigest(row.Source); err == nil {
						nativeDigest = sha
						sourceCache[row.Source] = sourceStamp{st.Size(), st.ModTime(), sha}
					}
				}
			}
		}
		if nativeDigest != "" && seenNative[nativeDigest] {
			continue
		}
		if nativeDigest != "" {
			seenNative[nativeDigest] = true
		}
		q.Items = append(q.Items, ReviewItem{Session: row.ID, Agent: row.Agent, Start: start, End: end, Stratum: strata, SourceDigest: digest, RepositoryHash: rulepack.Digest(row.Project), NativeSourceAvailable: available, HasAppliedPatch: applied, LabelStatus: "awaiting_two_independent_humans"})
		q.Items[len(q.Items)-1].NativeSourceSHA256 = nativeDigest
		q.Platforms[row.Agent]++
		if strata == "candidate" {
			q.Candidates++
		} else {
			q.Controls++
		}
	}
	q.Digest = eval.ReviewQueueDigest(q)
	return q, nil
}
