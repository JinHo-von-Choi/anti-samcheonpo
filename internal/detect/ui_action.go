package detect

import (
	"path"
	"regexp"
	"strings"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
)

// RuleUnrequestedUI is a write that adds several click targets (buttons,
// links, click handlers) to a screen when no request mentioned any. Each
// one is something the user has to read and decide about; one with no task
// behind it only fills space.
const RuleUnrequestedUI = "s3.unrequested_ui_action"

// How many new click targets one write may add before it is reported: a new
// screen needs a few to work at all, an edit usually needs one.
const (
	uiNewFileAllowance = 4
	uiEditAllowance    = 1
)

var uiExt = map[string]bool{".html": true, ".htm": true, ".jsx": true, ".tsx": true, ".vue": true, ".svelte": true, ".astro": true, ".erb": true, ".hbs": true, ".twig": true}

var (
	clickTargetRe = regexp.MustCompile(`(?i)<button\b|<a\s[^>]*\bhref\s*=|\bonClick\s*=|@click\b|\bv-on:click\b|\bon:click\b|role\s*=\s*["']button["']|<input\b[^>]*\btype\s*=\s*["'](?:submit|button)["']`)
	uiSpecRe      = regexp.MustCompile(`(?i)플랜|계획|기획|설계|명세|스펙|시안|디자인|목업|피그마|\b(plan|spec|design|mockup|figma|prd)s?\b`)
	uiRequestRe   = regexp.MustCompile(`(?i)버튼|링크|클릭|메뉴|탭|내비|네비|바로가기|CTA|\b(button|link|click|menu|tab|nav|navigation|cta|call to action)s?\b`)
)

// statedTargets are the click targets a request can put a number on.
var statedTargets = []struct {
	element string
	re      *regexp.Regexp
}{
	{"버튼", regexp.MustCompile(`(?i)<button\b|role\s*=\s*["']button["']|<input\b[^>]*\btype\s*=\s*["'](?:submit|button)["']`)},
	{"링크", regexp.MustCompile(`(?i)<a\s[^>]*\bhref\s*=`)},
}

var (
	deadTargetRe = regexp.MustCompile(`(?i)<a\b[^>]*\bhref\s*=\s*["'](?:#|javascript:[^"']*)?["']`)
	clickLabelRe = regexp.MustCompile(`(?is)<(?:button|a)\b([^>]*)>\s*([^<{]{2,40}?)\s*</(?:button|a)>`)
	clickGoalRe  = regexp.MustCompile(`(?i)(?:href|onClick|@click|on:click)\s*=\s*(?:"([^"]*)"|'([^']*)'|\{([^}]*)\})`)
	// a dialog's own controls repeat by design
	dialogLabelRe = regexp.MustCompile(`(?i)^(닫기|취소|확인|close|cancel|ok)$`)
	// a block styled as a card or notice, up to its first closing tag
	infoBlockRe = regexp.MustCompile(`(?is)<(div|section|aside|article)\b[^>]*\bclass(?:Name)?\s*=\s*["'][^"']*\b(card|notice|tip|tips|hint|info|callout|banner)\b[^"']*["'][^>]*>(.*?)</(?:div|section|aside|article)>`)
	// what makes a block more than words on a screen
	blockActionRe = regexp.MustCompile(`(?i)<(a|button|input|select|textarea|form|img|table|svg|canvas)\b|\{|@click|on:click`)
	tagRe         = regexp.MustCompile(`<[^>]*>`)
	cardRequestRe = regexp.MustCompile(`(?i)카드|안내|설명|도움말|공지|배너|팁|\b(cards?|notices?|tips?|hints?|callouts?|banners?|onboarding|help text|empty state)\b`)
)

// fillerCards is how many text-only info blocks one write may add before
// they are filling the screen.
const fillerCards = 2

func fillerCardCount(added string) int {
	n := 0
	for _, m := range infoBlockRe.FindAllStringSubmatch(added, -1) {
		if !blockActionRe.MatchString(m[3]) && len([]rune(strings.TrimSpace(tagRe.ReplaceAllString(m[3], "")))) >= 8 {
			n++
		}
	}
	return n
}

// repeatedCTA is how many click targets may share a label and a destination
// in one write before they are one action offered over and over.
const repeatedCTA = 3

// uiIssue finds, in what a write adds, a link that goes nowhere or one
// action offered again and again.
func uiIssue(added string) (issue, label string, count int) {
	if n := len(deadTargetRe.FindAllString(added, -1)); n > 0 {
		return "dead_target", "", n
	}
	labels := map[string]int{}
	for _, m := range clickLabelRe.FindAllStringSubmatch(added, -1) {
		goal := clickGoalRe.FindString(m[1])
		if goal != "" && !dialogLabelRe.MatchString(m[2]) {
			labels[m[2]+"\x00"+goal]++
		}
	}
	for _, l := range sortedKeys(labels) {
		if labels[l] >= repeatedCTA {
			return "repeated_cta", strings.SplitN(l, "\x00", 2)[0], labels[l]
		}
	}
	return "", "", 0
}

func (e *Engine) observeUIIssues(ev *event.Event, sigs *[]Signal) {
	for _, pf := range ev.Patch {
		if pf.Deleted || !uiExt[strings.ToLower(path.Ext(pf.Path))] || metaExempt(pf.Path) {
			continue
		}
		added := strings.Join(pf.Added, "\n")
		issue, label, count := uiIssue(added)
		if n := fillerCardCount(added); issue == "" && n >= fillerCards && !cardRequestRe.MatchString(e.RequestText()) {
			issue, count = "filler_card", n
		}
		key := pf.Path + "\x00issue"
		if n, ok := e.St.uiFlagged[key]; issue == "" || ok && n == len(e.Requests) {
			continue
		}
		if e.St.uiFlagged == nil {
			e.St.uiFlagged = map[string]int{}
		}
		e.St.uiFlagged[key] = len(e.Requests)
		e.add(sigs, Signal{Detector: "S3", Rule: RuleUnrequestedUI, Confidence: 0.6, Level: L1, Evidence: []int64{ev.Seq}, Estimate: true,
			Facts: map[string]any{"path": pf.Path, "count": count, "issue": issue, "label": label}})
	}
}

// observeUIActions reports a write to a screen file that adds more click
// targets than the request allows: more than it numbered, or, when it named
// none, more than the allowance. A file is reported once until the user makes
// another request.
func (e *Engine) observeUIActions(ev *event.Event, sigs *[]Signal) {
	if ev.Category != event.CatProduce || (ev.Tool != event.ToolWrite && ev.Tool != event.ToolEdit) {
		return
	}
	e.observeUIIssues(ev, sigs)
	req := e.RequestText()
	stated := intent.InferOutput(req)
	for _, t := range statedTargets {
		if _, ok := stated.Count(t.element); ok {
			e.observeStatedUI(ev, stated, sigs)
			return
		}
	}
	if uiRequestRe.MatchString(req) || uiSpecRe.MatchString(req) {
		// the request names the elements, or points at a plan or design
		// that does
		return
	}
	for _, pf := range ev.Patch {
		if pf.Deleted || !uiExt[strings.ToLower(path.Ext(pf.Path))] || metaExempt(pf.Path) {
			continue
		}
		if n, ok := e.St.uiFlagged[pf.Path]; ok && n == len(e.Requests) {
			continue
		}
		added := len(clickTargetRe.FindAllString(strings.Join(pf.Added, "\n"), -1)) - len(clickTargetRe.FindAllString(strings.Join(pf.Removed, "\n"), -1))
		created := contains(ev.Created, pf.Path)
		allow := uiEditAllowance
		if created {
			allow = uiNewFileAllowance
		}
		if added <= allow {
			continue
		}
		if e.St.uiFlagged == nil {
			e.St.uiFlagged = map[string]int{}
		}
		e.St.uiFlagged[pf.Path] = len(e.Requests)
		e.add(sigs, Signal{Detector: "S3", Rule: RuleUnrequestedUI, Confidence: 0.6, Level: L1, Evidence: []int64{ev.Seq}, Estimate: true,
			Facts: map[string]any{"path": pf.Path, "count": added, "created": created}})
	}
}

// observeStatedUI adds up what the writes since the latest request added to
// each screen file and reports the file once that passes the requested
// number.
func (e *Engine) observeStatedUI(ev *event.Event, stated intent.Output, sigs *[]Signal) {
	if e.St.uiNet == nil || e.St.uiNetRequests != len(e.Requests) {
		e.St.uiNet, e.St.uiNetRequests = map[string]int{}, len(e.Requests)
	}
	for _, pf := range ev.Patch {
		if pf.Deleted || !uiExt[strings.ToLower(path.Ext(pf.Path))] || metaExempt(pf.Path) {
			continue
		}
		if n, ok := e.St.uiFlagged[pf.Path]; ok && n == len(e.Requests) {
			continue
		}
		for _, t := range statedTargets {
			limit, ok := stated.Count(t.element)
			if !ok {
				continue
			}
			key := pf.Path + "\x00" + t.element
			e.St.uiNet[key] += len(t.re.FindAllString(strings.Join(pf.Added, "\n"), -1)) - len(t.re.FindAllString(strings.Join(pf.Removed, "\n"), -1))
			if e.St.uiNet[key] <= limit {
				continue
			}
			if e.St.uiFlagged == nil {
				e.St.uiFlagged = map[string]int{}
			}
			e.St.uiFlagged[pf.Path] = len(e.Requests)
			e.add(sigs, Signal{Detector: "S3", Rule: RuleUnrequestedUI, Confidence: 0.6, Level: L1, Evidence: []int64{ev.Seq}, Estimate: true,
				Facts: map[string]any{"path": pf.Path, "count": e.St.uiNet[key], "element": t.element, "requested": limit}})
			break
		}
	}
}
