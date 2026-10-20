package live

import (
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intervene"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/notify"
)

// hudPresets are the one-press commands that cover a standing finding.
var hudPresets = []string{"/samcheonpo:keep now", "/samcheonpo:keep normal", "/samcheonpo:steer", "/samcheonpo:summary"}

// hudState is the one source of the session's user-facing state: the status
// line renders from it and the HUD broadcasts it, so the two never disagree.
// Missing measurement is shown as unknown, never as zero spend. Called with
// s.mu held.
func (s *Session) hudState() notify.HUDState {
	st := s.eng.St
	h := notify.HUDState{SessionID: s.ID}
	var parts []string
	if n := s.queueRejected.Load(); n > 0 {
		parts = append(parts, fmt.Sprintf("[관측 큐 포화: %d건 미처리]", n))
	}
	if s.storageErr != nil {
		parts = append(parts, "[기록 저장 실패]")
	}
	if s.Cfg.Experiment.Enabled {
		parts = append(parts, "[실험 모드: 일부 안내 보류]")
	}
	if s.shapeNote != "" {
		parts = append(parts, "[훅 형식 불일치: 관찰만 함 ("+s.shapeNote+")]")
	}
	if n := s.preLate.Load(); n > 0 {
		parts = append(parts, fmt.Sprintf("[실행 전 판정 시간 초과 %d건: 그 판정은 전달되지 않았을 수 있음]", n))
	}
	met, total := s.criteria()
	switch {
	case total > 0:
		h.Progress = fmt.Sprintf("진척 %d/%d", met, total)
	case len(st.ProgressSeqs) > 0:
		h.Progress = "진척 추정"
	default:
		h.Progress = "진척 측정 불가"
	}
	parts = append(parts, h.Progress)
	tokens, unpriced, waste, _ := s.measuredUsage()
	h.UsageUnknown = tokens == 0 || unpriced > 0
	if h.UsageUnknown {
		parts = append(parts, "공회전 비용 미확인")
	} else {
		h.IdleKRW = cost.Won(st.TotalMicro - st.ProgressMark)
		parts = append(parts, "공회전 API환산 "+contract.Comma(h.IdleKRW)+"원")
	}
	pct := 0
	if st.TotalMicro > 0 {
		pct = int(float64(waste) * 100 / float64(st.TotalMicro))
	}
	if h.UsageUnknown || st.TotalMicro == 0 {
		parts = append(parts, "헛짓 비율 미확인")
	} else {
		h.WasteKRW = cost.Won(waste)
		h.WastePercentage = pct
		h.WasteRatioText, h.WasteRatioBasis = cost.RatioText(waste, st.TotalMicro), "money"
		parts = append(parts, "낭비 분류율 "+h.WasteRatioText+" (계측 금액)")
	}
	if active, tok, tier := s.eng.SessionLength(); tier > 0 {
		h.SessionMinutes, h.SessionTokens, h.SessionTier = int(active.Minutes()), tok, tier
		label := "세션"
		if tier >= 3 {
			label = "세션 상한 도달"
		}
		parts = append(parts, fmt.Sprintf("%s %s·%s 토큰", label, detect.HoursText(active.Hours()), detect.TokensText(tok)))
	}
	if spent, limit, ok := s.treeSpend(); ok {
		h.TreeKRW, h.TreeLimitKRW = spent, limit
		parts = append(parts, fmt.Sprintf("군집 지출 %s원/%s원", contract.Comma(spent), contract.Comma(limit)))
	}
	assessment, work := s.currentAssessment(met, total)
	h.Assessment = &assessment
	h.Linked = s.linkedSummary
	h.Alignment = s.alignmentGraph
	if h.Alignment == nil {
		g := s.requirementGraph()
		s.alignmentGraph = &g
		h.Alignment = &g
	}
	if len(h.Alignment.Requirements) > 0 {
		work.Remaining = nil
		for _, r := range h.Alignment.Requirements {
			label := r.Label
			if r.Status == "passing_evidence_candidate" {
				label += " · 통과 근거의 현재성 확인"
			}
			work.Remaining = append(work.Remaining, label)
		}
	}
	if s.linkedUnavailable {
		assessment.Reasons["linked_unavailable"] = 1
	}
	// A lost filesystem watch makes the workspace fingerprint unknown, not
	// clean. Reporting it here keeps "available" from hiding a blind
	// monitor.
	switch s.watchState() {
	case WatchStateUnavailable:
		assessment.Reasons["watch_unavailable"] = 1
	case WatchStateDegraded:
		assessment.Reasons["watch_degraded"] = 1
	}
	// A learned rule pack that exists but cannot be read is not the same
	// as having no learned rules. Reporting the difference keeps a broken
	// file from looking like a quiet monitor.
	switch s.candidatePackState {
	case "unreadable":
		assessment.Reasons["learned_rules_unreadable"] = 1
	case "invalid":
		assessment.Reasons["learned_rules_invalid"] = 1
	}
	h.Work = &work
	if s.eng.Research != nil {
		research := s.eng.Research.Snapshot()
		h.Research = &research
	}
	plan := s.verificationCandidates()
	h.Verification = &plan
	if h.UsageUnknown {
		h.WasteRatioText, h.WasteRatioBasis = assessment.WasteRatio, assessment.CostBasis
	}
	if assessment.CostAttribution == "unattributed_findings" {
		parts = append(parts, "지적 비용 귀속 미확인")
	}
	if assessment.Findings > 0 {
		parts = append(parts, fmt.Sprintf("판정 %d", assessment.Findings))
	}
	h.StatusText = s.stateLabel(met, total)
	h.Summary = strings.Join(parts, " · ")
	h.IsBlocked = s.unresolved != nil
	h.Requested = s.firstPrompt
	if s.intentRevision != nil {
		h.Requested = s.intentRevision.Goal
	}
	if s.c != nil && s.c.Goal != "" && s.acc.RequestedGoal == "" && (s.intentRevision == nil || s.intentRevision.Goal == s.c.Goal) {
		h.Requested = s.c.Goal
	}
	if p := s.lastPrimary; p != nil && p.Seq > st.LastProgress && p.Level >= detect.L1 {
		ctx := s.msgContext()
		h.Observation = firstLine(intervene.User(*p, ctx))
		h.RecommendedAction = intervene.Say(*p, ctx)
		h.PresetCommands = append([]string(nil), hudPresets...)
	}
	if n := len(st.Events); n > 0 {
		if ev := st.Events[n-1]; ev.Summary != "" {
			h.Current = ev.Summary
		}
	}
	return h
}

// hudPayload is the current HUD payload for a reader that polls. Called
// without s.mu.
func (s *Session) hudPayload() notify.HUDPayload {
	s.mu.Lock()
	defer s.mu.Unlock()
	return notify.BuildStateUpdate(s.hudState())
}

// broadcastHUD publishes the current state to HUD subscribers. The send never
// blocks, so it is safe on the hook path. Called with s.mu held.
func (s *Session) broadcastHUD() {
	s.reportSpend()
	if s.hud != nil {
		s.hud.Broadcast(s.hudState())
	}
}

// streamHUD answers an HUDStream request: the current state at once, then
// every change as one JSON line, until the reader goes away. It never leaves
// the local socket.
func (d *Daemon) streamHUD(c net.Conn, req Request) {
	var in struct {
		SessionID string `json:"session_id"`
		Root      string `json:"root"`
	}
	_ = json.Unmarshal(req.Payload, &in)
	s := d.latest(in.Root, in.SessionID)
	enc := json.NewEncoder(c)
	if s == nil {
		_ = enc.Encode(Response{V: ProtocolVersion, Error: "지켜볼 세션이 없다"})
		return
	}
	ch, cancel := s.hud.Subscribe()
	defer cancel()
	_ = c.SetDeadline(time.Time{})
	if err := enc.Encode(s.hudPayload()); err != nil {
		return
	}
	for {
		select {
		case p, ok := <-ch:
			if !ok {
				return
			}
			if err := enc.Encode(p); err != nil {
				return
			}
		case <-d.done:
			return
		}
	}
}
