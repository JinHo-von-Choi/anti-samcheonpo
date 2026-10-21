package cli

import "fmt"

// benchHookVerdict is what one bench-hook round measured.
//
// The round separates two failure kinds that used to share one counter.
// A fast failure is the hook that could not do its job: it could not
// connect, the daemon answered with an error, or the answer could not be
// written out. A timeout is the hook that was slow: the socket deadline
// expired before the daemon answered.
//
// Only the second kind is a latency question. Only the first kind may
// never pass as a latency pass, so the two are counted apart.
type benchHookVerdict struct {
	N int
	// Responses is the number of rounds the hook answered.
	Responses int
	// Timeouts is the number of rounds that ran past the socket deadline.
	Timeouts int
	// FastFailures is the number of rounds that failed without a timeout.
	FastFailures int
	// NoResponse is the number of rounds with no answer at all.
	NoResponse int
	// FirstOK and FirstResponse describe the cold-start hook.
	FirstOK       bool
	FirstResponse bool
	// ColdLate marks a cold start that outran its own deadline while the
	// daemon was still booting. That is the designed behaviour, not a
	// failure.
	ColdLate bool
	// Prompts, ToolCalls and QueueRejected are the daemon's own counts.
	Prompts       int
	ToolCalls     int
	QueueRejected uint64
}

// benchHookTolerance is how many slow rounds may be tolerated.
//
// The default is one percent of the rounds and never less than one. A
// hosted runner's process start time varies with load, so an isolated
// startup stall is not a regression of the monitor — measured on this host,
// the handling span p95 stayed at 2.6ms while the failing round's total
// jumped to 20.9ms.
//
// A systematic slowdown is still caught: the handling span p95 limit, the
// daemon's own counts and the fast-failure rule are absolute.
func benchHookTolerance(n, maxTimeouts int) int {
	if maxTimeouts >= 0 {
		return maxTimeouts
	}
	if t := n / 100; t > 1 {
		return t
	}
	return 1
}

// checkBenchHook judges one round.
//
// It returns the reason the round must not pass, or an empty reason when
// the round passes. The note explains a pass that tolerated slow rounds,
// so a tolerated stall is never silent.
func checkBenchHook(v benchHookVerdict, maxTimeouts int) (string, string) {
	if !v.ColdLate && (!v.FirstOK || !v.FirstResponse) {
		return "콜드 스타트의 훅이 응답하지 못했다", ""
	}
	if v.FastFailures > 0 {
		return fmt.Sprintf("빠른 실패 %d회가 있어 지연 통과로 인정하지 않는다", v.FastFailures), ""
	}
	tol := benchHookTolerance(v.N, maxTimeouts)
	if v.Timeouts > tol {
		return fmt.Sprintf("타임아웃 %d회가 허용 한도 %d회를 넘는다", v.Timeouts, tol), ""
	}
	if v.NoResponse != v.Timeouts {
		return fmt.Sprintf("응답 없는 요청 %d건 중 타임아웃 %d건 외에 %d건이 있다", v.NoResponse, v.Timeouts, v.NoResponse-v.Timeouts), ""
	}
	if v.Prompts != 1 {
		return fmt.Sprintf("데몬이 첫 프롬프트를 %d번 기록했다", v.Prompts), ""
	}
	if v.ToolCalls != v.N {
		return fmt.Sprintf("데몬이 도구 호출을 %d/%d 건 받았다", v.ToolCalls, v.N), ""
	}
	if v.QueueRejected > 0 {
		return fmt.Sprintf("데몬이 %d건을 큐에서 거절했다", v.QueueRejected), ""
	}
	if v.Timeouts > 0 {
		return "", fmt.Sprintf("느린 기동 %d회를 허용 한도 %d회 이내에서 견뎠다 (느림이지 훅이 일을 못 한 것은 아니다; 빠른 실패·데몬 수신·처리 구간 기준은 그대로 검사)", v.Timeouts, tol)
	}
	return "", ""
}
