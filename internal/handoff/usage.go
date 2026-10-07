package handoff

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
	"math"
)

// Usage is a measured snapshot, not a billing amount. Nil is unmeasured.
type Usage struct {
	Session            SessionRef `json:"session"`
	Tokens             *int64     `json:"tokens"`
	APIEquivalentMicro *int64     `json:"api_equivalent_micro_krw"`
}

type UsageTotal struct {
	Tokens             *int64       `json:"tokens"`
	APIEquivalentMicro *int64       `json:"api_equivalent_micro_krw"`
	Counted            []SessionRef `json:"counted"`
	CoveredByParent    []SessionRef `json:"covered_by_parent"`
	Unknown            []string     `json:"unknown"`
}

// AggregateUsage requires one explicit connected root. A parent that declares
// inclusive usage covers descendants; they must not be counted a second time.
// The links are declarations, not proof that a provider's accounting is correct.
func AggregateUsage(taskID string, links []intent.SessionLink, samples []Usage) UsageTotal {
	var out UsageTotal
	unknown := func(reason string) UsageTotal {
		out.Tokens = nil
		out.APIEquivalentMicro = nil
		out.Unknown = append(out.Unknown, reason)
		return out
	}
	byID := map[SessionRef]intent.SessionLink{}
	roots := 0
	for _, link := range links {
		id := SessionRef{Agent: link.Agent, ID: link.SessionID}
		if taskID == "" || link.TaskID != taskID || id.Agent == "" || id.ID == "" {
			return unknown("작업·세션 연결 미확인")
		}
		if old, ok := byID[id]; ok {
			if old != link {
				return unknown("동일 세션의 연결이 충돌함")
			}
			continue
		}
		if (link.ParentAgent == "") != (link.ParentSessionID == "") {
			return unknown("부모 식별자가 불완전함")
		}
		byID[id] = link
		if link.ParentSessionID == "" {
			roots++
		}
	}
	if roots != 1 {
		return unknown("단일 루트와 모든 부모 관계를 확인하지 못함")
	}
	measured := map[SessionRef]Usage{}
	equal := func(a, b *int64) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
	for _, sample := range samples {
		if _, ok := byID[sample.Session]; !ok {
			return unknown("작업에 연결되지 않은 사용량이 있음")
		}
		if sample.Tokens != nil && *sample.Tokens < 0 || sample.APIEquivalentMicro != nil && *sample.APIEquivalentMicro < 0 {
			return unknown("음수 사용량은 합산할 수 없음")
		}
		if old, ok := measured[sample.Session]; ok && (!equal(old.Tokens, sample.Tokens) || !equal(old.APIEquivalentMicro, sample.APIEquivalentMicro)) {
			return unknown("중복 사용량 스냅샷 충돌")
		}
		measured[sample.Session] = sample
	}
	var tokens, micro int64
	tokensKnown, microKnown := true, true
	seenOutput := map[SessionRef]bool{}
	for _, link := range links {
		id := SessionRef{Agent: link.Agent, ID: link.SessionID}
		if seenOutput[id] {
			continue
		}
		seenOutput[id] = true
		path := map[SessionRef]bool{id: true}
		parent := link
		covered := false
		for parent.ParentSessionID != "" {
			pid := SessionRef{Agent: parent.ParentAgent, ID: parent.ParentSessionID}
			if path[pid] {
				return unknown("순환 부모 관계")
			}
			path[pid] = true
			var ok bool
			parent, ok = byID[pid]
			if !ok {
				return unknown("부모 세션이 누락됨")
			}
			covered = covered || parent.IncludesChildren
		}
		if covered {
			out.CoveredByParent = append(out.CoveredByParent, id)
			continue
		}
		out.Counted = append(out.Counted, id)
		sample, ok := measured[id]
		if !ok || sample.Tokens == nil {
			tokensKnown = false
		} else if *sample.Tokens > math.MaxInt64-tokens {
			return unknown("토큰 합계 범위 초과")
		} else {
			tokens += *sample.Tokens
		}
		if !ok || sample.APIEquivalentMicro == nil {
			microKnown = false
		} else if *sample.APIEquivalentMicro > math.MaxInt64-micro {
			return unknown("환산액 합계 범위 초과")
		} else {
			micro += *sample.APIEquivalentMicro
		}
	}
	if tokensKnown {
		out.Tokens = &tokens
	} else {
		out.Unknown = append(out.Unknown, "일부 세션의 토큰 미계측")
	}
	if microKnown {
		out.APIEquivalentMicro = &micro
	} else {
		out.Unknown = append(out.Unknown, "일부 세션의 API 환산액 미확인")
	}
	return out
}
