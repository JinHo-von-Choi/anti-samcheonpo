package bench

import (
	"fmt"
	"math"
	"math/rand"
	"sort"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
)

// ClusterComparison is exploratory paired task-cluster inference. Repeated
// runs stay in their task cluster; they never become independent tasks.
// Intervals are Bonferroni-adjusted across the compared non-baseline arms.
type ClusterComparison struct {
	Method            string         `json:"method"`
	Tasks             int            `json:"tasks"`
	Blocks            int            `json:"blocks"`
	Resamples         int            `json:"resamples"`
	Seed              int64          `json:"seed"`
	Confidence        float64        `json:"confidence"`
	SuccessDifference *eval.Interval `json:"success_difference_ci,omitempty"`
	CostReduction     *eval.Interval `json:"cost_reduction_ci,omitempty"`
	Reason            string         `json:"withheld_reason,omitempty"`
}

type pairedTrial struct{ base, other TrialResult }

func compareClusters(rows []TrialResult, base, arm string, comparisons int) ClusterComparison {
	c := ClusterComparison{Method: "paired-task-cluster-percentile/1", Seed: 73129, Resamples: 4000, Confidence: 1 - 0.05/float64(max(1, comparisons))}
	if err := ValidateRows(rows); err != nil {
		c.Reason = err.Error()
		return c
	}
	by := map[string]map[string]TrialResult{}
	conditions := map[string]string{}
	metadata := map[string]string{}
	taskHash := map[string]string{}
	taskGroup := map[string]string{}
	for _, r := range rows {
		if r.Arm != base && r.Arm != arm {
			continue
		}
		if r.RunID == "" || r.Task == "" || r.TaskDigest == "" || r.GraderDigest == "" || !r.IndependentGrader || r.ProtocolVersion != "2" || r.AgentVersion == "" || r.Model == "" || r.PolicyVersion == "" || r.PriceVersion == "" || r.EvaluatorVersion == "" {
			c.Reason = "실험 식별자·버전·독립 채점 근거 누락"
			return c
		}
		meta := r.Agent + "/" + r.AgentVersion + "/" + r.Model + "/" + r.PolicyVersion
		conditions[r.Arm] = r.Agent + "/" + r.AgentVersion + "/" + r.Model
		if old, ok := metadata[r.Arm]; ok && old != meta {
			c.Reason = "실행 방식 안에서 모델/정책/에이전트 조건이 바뀜"
			return c
		}
		metadata[r.Arm] = meta
		hash := r.TaskDigest + "/" + r.GraderDigest
		if old, ok := taskHash[r.Task]; ok && old != hash {
			c.Reason = "과제 또는 채점기 버전 혼합"
			return c
		}
		taskHash[r.Task] = hash
		if old, ok := taskGroup[r.Task]; ok && old != clusterOf(r) {
			c.Reason = "같은 과제의 원본 사건 그룹이 시행마다 다름"
			return c
		}
		taskGroup[r.Task] = clusterOf(r)
		key := fmt.Sprintf("%q/%q/%d", r.RunID, r.Task, r.Trial)
		if by[key] == nil {
			by[key] = map[string]TrialResult{}
		}
		by[key][r.Arm] = r
	}
	if conditions[base] != conditions[arm] {
		c.Reason = "기준군과 후보군의 에이전트/모델이 달라 감시 정책 효과를 분리할 수 없음"
		return c
	}
	tasks := map[string][]pairedTrial{}
	for _, block := range by {
		a, aok := block[base]
		b, bok := block[arm]
		if !aok || !bok {
			c.Reason = "짝이 없는 시행 블록; 누락 시행을 버리지 않음"
			return c
		}
		if a.Seed != b.Seed {
			c.Reason = "짝 시행의 seed 불일치"
			return c
		}
		// variants of one source event resample together as one cluster
		tasks[clusterOf(a)] = append(tasks[clusterOf(a)], pairedTrial{a, b})
		c.Blocks++
	}
	names := make([]string, 0, len(tasks))
	for name := range tasks {
		names = append(names, name)
	}
	sort.Strings(names)
	c.Tasks = len(names)
	if c.Tasks < 12 || c.Blocks < MinTrials {
		c.Reason = "독립 과제(원본 사건 그룹) 12개와 짝 시행 30개 미만; 효과 판정 보류"
		return c
	}
	// Stable ordering keeps intervals invariant under JSONL input order.
	for _, name := range names {
		sort.Slice(tasks[name], func(i, j int) bool {
			a, b := tasks[name][i].base, tasks[name][j].base
			if a.RunID != b.RunID {
				return a.RunID < b.RunID
			}
			return a.Trial < b.Trial
		})
	}
	costKnown := true
	for _, block := range by {
		for _, r := range block {
			if !r.CostKnown || r.MonitorKRW == nil || *r.MonitorKRW < 0 {
				costKnown = false
			}
		}
	}
	rng := rand.New(rand.NewSource(c.Seed))
	diffs := make([]float64, 0, c.Resamples)
	reductions := make([]float64, 0, c.Resamples)
	for draw := 0; draw < c.Resamples; draw++ {
		var count, successA, successB, amountA, amountB float64
		for range names {
			cluster := tasks[names[rng.Intn(len(names))]]
			// Resample complete task clusters. Within-task repeats remain together.
			for _, p := range cluster {
				count++
				if p.base.Verified {
					successA++
				}
				if p.other.Verified {
					successB++
				}
				if costKnown {
					amountA += float64(p.base.TotalKRW) + float64(*p.base.MonitorKRW)
					amountB += float64(p.other.TotalKRW) + float64(*p.other.MonitorKRW)
				}
			}
		}
		diffs = append(diffs, (successB-successA)/count)
		if costKnown && successA > 0 && successB > 0 && amountA > 0 {
			reductions = append(reductions, 1-(amountB/successB)/(amountA/successA))
		}
	}
	tail := (1 - c.Confidence) / 2
	interval := func(v []float64) *eval.Interval {
		sort.Float64s(v)
		q := func(p float64) float64 {
			x := p * float64(len(v)-1)
			i := int(x)
			if i+1 == len(v) {
				return v[i]
			}
			return v[i] + (v[i+1]-v[i])*(x-float64(i))
		}
		return &eval.Interval{Lo: q(tail), Hi: q(1 - tail)}
	}
	c.SuccessDifference = interval(diffs)
	if c.SuccessDifference.Lo == c.SuccessDifference.Hi {
		c.SuccessDifference = nil
		c.Reason = "과제 간 성공률 차이 분산을 추정할 수 없음; 퇴화 구간으로 비열등을 선언하지 않음"
	}
	if len(reductions) == c.Resamples {
		c.CostReduction = interval(reductions)
		if c.CostReduction.Lo == c.CostReduction.Hi {
			c.CostReduction = nil
			c.Reason = "과제 간 비용 효과 분산을 추정할 수 없음"
		}
	} else if !costKnown {
		c.Reason = "에이전트/감시 비용 누락; 비용 구간 보류"
	} else {
		c.Reason = "완료 0 또는 기준 비용 0인 재표집 포함; 비용 구간 보류"
	}
	return c
}

// RequiredTaskPairs is a planning approximation for a paired task-level mean
// difference, not retrospective power or a guarantee for a cost ratio. SD must
// come from a separate pilot; all endpoints/arms must be predeclared.
func RequiredTaskPairs(sd, detectableGap, alpha, power float64, comparisons int) (int, error) {
	if math.IsNaN(sd) || math.IsNaN(detectableGap) || math.IsNaN(alpha) || math.IsNaN(power) || math.IsInf(sd, 0) || math.IsInf(detectableGap, 0) || sd <= 0 || detectableGap <= 0 || alpha <= 0 || alpha >= 0.5 || power <= 0.5 || power >= 1 || comparisons < 1 {
		return 0, fmt.Errorf("유효한 파일럿 표준편차·효과 여유·alpha·power·비교 수 필요")
	}
	z := func(p float64) float64 { return math.Sqrt2 * math.Erfinv(2*p-1) }
	n := math.Ceil(math.Pow((z(1-alpha/(2*float64(comparisons)))+z(power))*sd/detectableGap, 2))
	if math.IsInf(n, 0) || n > 1e7 {
		return 0, fmt.Errorf("요구 표본이 계획 상한을 넘음")
	}
	return max(12, int(n)), nil
}

// clusterOf is the independent unit of a trial: its source event group, or
// the task itself when the task declares none.
func clusterOf(r TrialResult) string {
	if r.Group != "" {
		return "group:" + r.Group
	}
	return "task:" + r.Task
}
