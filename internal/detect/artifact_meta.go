package detect

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
)

// RuleArtifactMeta is a shared artifact (document, code comment, commit, tag,
// pull request, release note) that carries sentences about how the work was
// done. Those sentences put the session's instructions and process into a
// record other people read.
const RuleArtifactMeta = "s5.artifact_meta"

// maxMetaHits bounds what one session remembers per file and per commit.
const maxMetaHits = 20

// metaState is what s5.artifact_meta remembers: the sentences written into
// shared files and committed messages that are still waiting to be published.
type metaState struct {
	docs   map[string][]MetaHit
	commit []MetaHit
}

func (e *Engine) meta() *metaState {
	if e.St.metaArt == nil {
		e.St.metaArt = &metaState{docs: map[string][]MetaHit{}}
	}
	return e.St.metaArt
}

// observeArtifactMeta follows writes and publishing commands. A write that
// adds such sentences to a document or a comment is reported once for that
// write; a later write that removes them clears them. A successful commit
// remembers its own sentences until it is amended or pushed.
func (e *Engine) observeArtifactMeta(ev *event.Event, sigs *[]Signal) {
	m := e.meta()
	switch {
	case ev.Category == event.CatProduce && (ev.Tool == event.ToolWrite || ev.Tool == event.ToolEdit):
		for _, pf := range ev.Patch {
			if pf.Deleted {
				delete(m.docs, pf.Path)
				continue
			}
			kept := m.docs[pf.Path][:0]
			for _, h := range m.docs[pf.Path] {
				if !removedSentence(h, pf.Removed) {
					kept = append(kept, h)
				}
			}
			hits := MetaSentences(SharedText(pf.Path, pf.Added))
			for i := range hits {
				hits[i].Path = pf.Path
			}
			m.docs[pf.Path] = capHits(append(kept, hits...))
			if len(m.docs[pf.Path]) == 0 {
				delete(m.docs, pf.Path)
			}
			if len(hits) > 0 {
				e.add(sigs, Signal{Detector: "S5", Rule: RuleArtifactMeta, Confidence: 0.7, Level: L1, Evidence: []int64{ev.Seq},
					Facts: metaFacts(hits, map[string]any{"kind": "document", "path": pf.Path})})
			} else if filler := fillerSentences(pf.Path, pf.Added, e.LatestRequest()); len(filler) > 0 {
				// a deletion candidate, not a leak: it is never remembered
				// for the commit or the push
				e.add(sigs, Signal{Detector: "S5", Rule: RuleArtifactMeta, Confidence: 0.55, Level: L1, Evidence: []int64{ev.Seq}, Estimate: true,
					Facts: metaFacts(filler, map[string]any{"kind": "filler", "path": pf.Path})})
			}
		}
	case ev.Tool == event.ToolShell && ev.ExitCode != nil && *ev.ExitCode == 0 && !ev.Background:
		kind, text := classify.Published(cmdOf(ev))
		switch kind {
		case classify.PublishCommit:
			// the documents' sentences were committed with it, or at least
			// judged at this commit: from here on they wait for the push
			hits := MetaSentences(text)
			for _, p := range sortedKeys(m.docs) {
				hits = append(hits, m.docs[p]...)
			}
			clear(m.docs)
			if classify.IsAmend(cmdOf(ev)) && text != "" {
				m.commit = hits
			} else {
				m.commit = capHits(append(m.commit, hits...))
			}
		}
		if classify.Pushes(cmdOf(ev)) {
			m.commit = nil
		}
	}
}

// publishCheck judges a command that publishes text before it runs: the
// message it carries, and for a commit or push the sentences still standing
// in written documents and committed messages. Every channel shares one
// target, so after the advice a commit or push that still carries such
// sentences is the same behavior repeated.
func (e *Engine) publishCheck(ev *event.Event, live bool) *Signal {
	if ev.Tool != event.ToolShell {
		return nil
	}
	kind, text := classify.Published(cmdOf(ev))
	if kind == classify.PublishNone {
		return nil
	}
	hits := MetaSentences(text)
	m := e.meta()
	if kind == classify.PublishCommit || kind == classify.PublishPush {
		for _, p := range sortedKeys(m.docs) {
			hits = append(hits, m.docs[p]...)
		}
	}
	if classify.Pushes(cmdOf(ev)) || kind == classify.PublishCommit && classify.IsAmend(cmdOf(ev)) && text == "" {
		// a push publishes the commits; an amend that keeps the message
		// keeps its sentences
		hits = append(hits, m.commit...)
	}
	if len(hits) == 0 {
		return nil
	}
	facts := metaFacts(capHits(hits), map[string]any{"kind": "publish", "via": kind, "target": "publish", "cmd": ev.CmdNorm})
	if live {
		facts["blocked"] = true
	}
	return &Signal{Detector: "S5", Rule: RuleArtifactMeta, Confidence: 0.75, Level: L1, Evidence: []int64{ev.Seq}, Facts: facts}
}

// MetaCandidates renders hits for a message: where each sentence is and what
// to do with it. lineOf finds a sentence's line in its file; nil or 0 leaves
// the line out.
func MetaCandidates(hits []MetaHit, lineOf func(path, sentence string) int) string {
	lines := make([]string, 0, 6)
	for i, h := range hits {
		if i >= 5 {
			lines = append(lines, "…")
			break
		}
		where := h.Path
		if where != "" && lineOf != nil {
			if n := lineOf(h.Path, h.Sentence); n > 0 {
				where = fmt.Sprintf("%s:%d", h.Path, n)
			}
		}
		line := h.Sentence
		if where != "" {
			line = where + ": " + line
		}
		if h.Fix != "" {
			line += " → 고친 문장: " + h.Fix
		} else {
			line += " → 삭제"
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, " / ")
}

func metaFacts(hits []MetaHit, facts map[string]any) map[string]any {
	kinds := map[string]bool{}
	for _, h := range hits {
		kinds[h.Kind] = true
	}
	facts["count"] = len(hits)
	facts["hits"] = hits
	facts["candidates"] = MetaCandidates(hits, nil)
	facts["meta_kinds"] = strings.Join(sortedKeys(kinds), ",")
	return facts
}

// removedSentence reports whether a write removed the line a sentence came
// from.
func removedSentence(h MetaHit, removed []string) bool {
	probe := strings.TrimSuffix(h.Sentence, "…")
	for _, l := range removed {
		if strings.Contains(l, probe) {
			return true
		}
	}
	return false
}

func capHits(h []MetaHit) []MetaHit {
	if len(h) > maxMetaHits {
		return h[len(h)-maxMetaHits:]
	}
	return h
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
