// Package research relates observed sources to claims without declaring them true.
package research

import (
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/alignment"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

type Link = event.ResearchLink
type Question struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	State string `json:"state"`
}
type State struct {
	Questions        []Question `json:"questions"`
	Links            []Link     `json:"links"`
	Sources          int        `json:"observed_sources"`
	ContentResults   int        `json:"distinct_content_results"`
	Hostnames        int        `json:"distinct_hostnames"`
	DuplicateResults int        `json:"duplicate_results"`
	Completion       string     `json:"completion"`
	SourceLimit      bool       `json:"source_limit,omitempty"`
}
type source struct {
	seq     int64
	content string
}
type Tracker struct {
	Scope          string
	Goal           string
	State          State
	sources        map[string]source
	content, hosts map[string]bool
}

var webRefs = regexp.MustCompile(`https?://[^\s<>"'\)\]]+`)
var markdownRefs = regexp.MustCompile(`\[[^\]\n]{1,200}\]\(([^\)\n]+)\)`)
var exclusion = regexp.MustCompile(`(?i)(배제|반증|원인.*아님|ruled out|falsif|disprov)`)

func Reference(raw string) (string, string) {
	if len(raw) > 2048 {
		return "", ""
	}
	raw = strings.TrimSpace(raw)
	if u, err := url.Parse(raw); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" {
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
		u.Fragment = ""
		u.User = nil
		u.RawQuery = u.Query().Encode()
		return fp.Hash("web-source", u.String()), fp.Hash("host", u.Hostname())
	}
	if strings.Contains(raw, "://") || raw == "" {
		return "", ""
	}
	return fp.Hash("file-source", filepath.ToSlash(filepath.Clean(raw))), ""
}
func References(text string) []event.ResearchSource {
	if len(text) > 65536 {
		text = text[:65536]
	}
	seen := map[string]bool{}
	var out []event.ResearchSource
	add := func(raw string) {
		id, host := Reference(raw)
		if id != "" && !seen[id] && len(out) < 16 {
			out = append(out, event.ResearchSource{ReferenceHash: id, HostHash: host})
			seen[id] = true
		}
	}
	for _, raw := range webRefs.FindAllString(text, 16) {
		add(raw)
	}
	for _, m := range markdownRefs.FindAllStringSubmatch(text, 16) {
		add(m[1])
	}
	return out
}
func New(goal, scope string) *Tracker {
	t := &Tracker{Scope: scope, Goal: goal, sources: map[string]source{}, content: map[string]bool{}, hosts: map[string]bool{}, State: State{Completion: "unknown", Links: []Link{}, Questions: []Question{}}}
	for _, r := range alignment.Requirements(goal, nil, false) {
		t.State.Questions = append(t.State.Questions, Question{ID: r.ID, Label: r.Label, State: "unresolved_or_unverified"})
	}
	return t
}
func (t *Tracker) Observe(e *event.Event) {
	if e.Kind == event.KindTool && e.Category == event.CatExplore && !e.IsError && e.ResultObserved && e.ObservationFP != "" {
		if len(e.ResearchSources) == 0 {
			return
		}
		duplicate := t.content[e.ObservationFP]
		if !duplicate && len(t.content) < 512 {
			t.content[e.ObservationFP] = true
		}
		if duplicate {
			t.State.DuplicateResults++
		}
		for _, r := range e.ResearchSources {
			if _, exists := t.sources[r.ReferenceHash]; !exists && len(t.sources) >= 512 {
				t.State.SourceLimit = true
				continue
			}
			t.sources[r.ReferenceHash] = source{e.Seq, e.ObservationFP}
			if r.HostHash != "" && len(t.hosts) < 512 {
				t.hosts[r.HostHash] = true
			}
		}
	}
	if (e.Kind == event.KindMessage || e.Kind == event.KindTool && e.Category == event.CatProduce) && e.Text != "" && len(e.ResearchLinks) == 0 {
		kind := "source_link"
		if exclusion.MatchString(e.Text) {
			kind = "hypothesis_exclusion_claim"
		}
		for _, r := range References(e.Text) {
			if observed, ok := t.sources[r.ReferenceHash]; ok {
				link := Link{Claim: e.Seq, SourceHash: r.ReferenceHash, Evidence: observed.seq, Kind: kind, Meaning: "observed_source_link_not_verified_claim"}
				e.ResearchLinks = append(e.ResearchLinks, link)
			}
		}
	}
	for _, link := range e.ResearchLinks {
		if observed, ok := t.sources[link.SourceHash]; ok && link.Claim == e.Seq && observed.seq == link.Evidence && link.Meaning == "observed_source_link_not_verified_claim" {
			t.State.Links = append(t.State.Links, link)
		}
	}
	if len(t.State.Links) > 24 {
		t.State.Links = t.State.Links[len(t.State.Links)-24:]
	}
	t.State.Sources = len(t.sources)
	t.State.ContentResults = len(t.content)
	t.State.Hostnames = len(t.hosts)
}
func (t *Tracker) Snapshot() State {
	cp := t.State
	cp.Questions = append(cp.Questions[:0:0], cp.Questions...)
	cp.Links = append(cp.Links[:0:0], cp.Links...)
	return cp
}
