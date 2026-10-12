package otel

import (
	"encoding/json"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/assessment"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

// EncodeRedacted writes local OTLP without prompts, outputs, paths, commands or
// original conversation/call IDs. Nothing is sent to a collector.
func EncodeRedacted(s *event.Session) ([]byte, error) {
	cp := *s
	cp.ID = fp.Hash("conversation", s.ID)
	cp.ProjectPath = ""
	cp.FirstPrompt = ""
	cp.Events = nil
	for _, ev := range s.Events {
		e := *ev
		e.CmdNorm = ""
		e.Paths = nil
		e.CallID = ""
		e.RawTool = e.Tool
		cp.Events = append(cp.Events, &e)
	}
	b, err := Encode(&cp)
	if err != nil {
		return nil, err
	}
	var out Export
	if err = json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	for i := range out.ResourceSpans {
		r := &out.ResourceSpans[i]
		r.Resource.Attributes = []keyValue{kv("service.name", sv(s.Agent)), kv("samcheonpo.redacted", sv("true"))}
		for j := range r.ScopeSpans {
			for k := range r.ScopeSpans[j].Spans {
				sp := &r.ScopeSpans[j].Spans[k]
				var attrs []keyValue
				for _, a := range sp.Attributes {
					if a.Key != "gen_ai.tool.call.arguments" && a.Key != "gen_ai.tool.call.id" {
						attrs = append(attrs, a)
					}
				}
				sp.Attributes = attrs
			}
		}
	}
	return json.Marshal(out)
}

// EncodeRedactedAssessment adds derived coverage/cost/goal status as custom
// attributes to the root span. Original text and policy inputs stay local.
func EncodeRedactedAssessment(s *event.Session, a assessment.Summary) ([]byte, error) {
	b, err := EncodeRedacted(s)
	if err != nil {
		return nil, err
	}
	var out Export
	if err = json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	root := &out.ResourceSpans[0].ScopeSpans[0].Spans[0]
	root.Attributes = append(root.Attributes, kv("samcheonpo.assessment.state", sv(a.State)), kv("samcheonpo.assessment.candidates", iv(int64(a.Candidates))), kv("samcheonpo.assessment.confirmed", iv(int64(a.Confirmed))), kv("samcheonpo.assessment.findings", iv(int64(a.Findings))), kv("samcheonpo.cost.basis", sv(a.CostBasis)), kv("samcheonpo.cost.waste_ratio", sv(a.WasteRatio)), kv("samcheonpo.cost.attribution", sv(a.CostAttribution)), kv("samcheonpo.completion", sv(a.Completion)), kv("samcheonpo.coverage.shell_calls", iv(int64(a.Coverage.ShellCalls))), kv("samcheonpo.coverage.completed_shell_results", iv(int64(a.Coverage.CompletedShellResults))), kv("samcheonpo.coverage.total_collection", sv(a.Coverage.TotalCollection)))
	return json.Marshal(out)
}
