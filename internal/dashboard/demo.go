package dashboard

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/assessment"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
)

func demoDetail() Detail {
	exit := 1
	events := []*event.Event{
		{Seq: 0, Kind: event.KindPrompt, Summary: "로그인 실제 연동을 구현하고 검증하기"},
		{Seq: 1, Kind: event.KindTool, Tool: event.ToolRead, Category: event.CatExplore, Summary: "로그인 처리와 요청 조건 확인", WorkProgress: event.WorkProgress{Knowledge: true}},
		{Seq: 2, Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatVerify, Summary: "로그인 검사 실패: 응답 형식 불일치", ExitCode: &exit, Usage: event.Usage{In: 2000}, Priced: true, CostMicroKRW: 1200000},
		{Seq: 3, Kind: event.KindTool, Tool: event.ToolEdit, Category: event.CatProduce, Summary: "동일 요청의 응답 해석을 수정", WorkProgress: event.WorkProgress{Activity: true}},
		{Seq: 4, Kind: event.KindTool, Tool: event.ToolShell, Category: event.CatVerify, Summary: "같은 로그인 검사와 같은 실패가 반복", ExitCode: &exit, Usage: event.Usage{In: 1000}, Priced: true, CostMicroKRW: 800000},
	}
	verdicts := []detect.Signal{{Rule: "s2.behavior_loop", Detector: "S2", Seq: 4, Level: detect.L1, Estimate: true, Evidence: []int64{2, 4}, Facts: map[string]any{"kind": "demo"}}}
	a := assessment.BuildRecorded(events, verdicts, map[string]string{})
	d := Detail{Session: Session{ID: "demo-session", Agent: "Claude · Codex 예시", Goal: "로그인 기능을 실제 API와 연동하고 오류 처리와 검사를 완료해 주세요.", Mode: "demo"}, Demo: true, Assessment: &a, Findings: verdicts, Work: intent.InferWork("로그인 기능 구현")}
	for _, e := range events {
		d.Events = append(d.Events, EventView{Seq: e.Seq, Kind: e.Kind, Tool: e.Tool, Category: e.Category, Summary: e.Summary, Exit: e.ExitCode, Progress: e.WorkProgress})
	}
	d.Work.Remaining = []string{"로그인 응답 형식의 실패 원인 확인", "수정 뒤 관련 검사 통과 근거 확보"}
	d.Work.Phase = "verification"
	return d
}
