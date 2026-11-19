// Package correction turns the corrections a user gives the agent ("PR
// 만들지 마", "문서만 바꿨으면 시험 돌리지 마") into rules that can be
// checked. Only the kinds below are made: each one names a check point and
// what it matches, so an approved rule is enforced, not just remembered.
//
// The user's message is the only source; the agent's answers are not read.
// The message itself is not kept: a rule carries a statement written from
// the catalog and the hashes of the messages that gave it.
package correction

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/alignment"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
)

// Kinds and their check points.
const (
	// KindForbid is an action not to take; checked before the tool call.
	KindForbid = "forbid"
	// KindRequire is an action owed before the turn ends; checked at Stop
	// through the deliverable book.
	KindRequire = "require"
	// KindWriting is a rule about the answer's text; checked at Stop.
	KindWriting = "writing"
	// KindResource is a path not to use; checked before the tool call.
	KindResource = "resource"
)

// Actions beyond the deliverable actions of package alignment.
const (
	ActFullSuite     = "full_suite"
	ActTestAfterDocs = "test_after_docs"
	ActNewBranch     = "new_branch"
	ActKoreanAnswer  = "korean_answer"
	ActAvoidPath     = "avoid_path"
)

// States of a rule.
const (
	// StateObserved: given once, in one session, without a lasting scope.
	StateObserved = "observed"
	// StateCandidate: given with a lasting scope or in two sessions; it is
	// shown to the user and counted, not enforced.
	StateCandidate = "candidate"
	// StateActive: approved by the user; enforced.
	StateActive    = "active"
	StateWithdrawn = "withdrawn"
)

// Correction is one rule extracted from a user message.
type Correction struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`
	Action string `json:"action"`
	Target string `json:"target,omitempty"`
	// Persistent is set when the message gave a lasting scope ("앞으로",
	// "항상", "이 프로젝트에서는").
	Persistent bool   `json:"persistent"`
	Statement  string `json:"statement"`
}

var (
	lasting    = regexp.MustCompile(`(?i)앞으로|항상|매번|늘|언제나|이 ?프로젝트에서는|이후로|다음부터|절대로?|몇 ?번을|from now on|always|every time|in this (project|repo)`)
	fullSuite  = regexp.MustCompile(`(?i)(전체|전부|모든)\s*(시험|테스트|검사)\S*\s*(돌리지\s*마|하지\s*마|금지|말고)|(don'?t|do not|never)\s+run\s+(the\s+)?(full|whole|entire)\s+(test\s+)?suite`)
	afterDocs  = regexp.MustCompile(`(?i)(문서|README|주석|docs?)\S*\s*(만)?\s*(바꿨|고쳤|수정했|변경했)\S*\s*(는데|으면|면|으니)?.{0,20}(시험|테스트|검사)\S*\s*(돌리지\s*마|하지\s*마|왜|또|금지)`)
	newBranch  = regexp.MustCompile(`(?i)(브랜치\S*\s*(만들지\s*마|따지\s*마|만들지\s*말|생성하지\s*마)|현재\s*브랜치에\s*(커밋|작업)|(don'?t|do not|never)\s+(create|make)\s+(a\s+)?(new\s+)?branch)`)
	korean     = regexp.MustCompile(`(?i)한국어로\s*(답|써|말|해)|한국말로\s*(해|답|써)|(answer|reply|write)\s+in\s+korean`)
	avoidPath  = regexp.MustCompile("(?:`([^`\\s]+)`|(/[\\w.-]+(?:/[\\w.-]+)+))\\S*\\s*(?:은|는|에|에는|을|를)?\\s*(?:쓰지\\s*마|사용하지\\s*마|건드리지\\s*마|저장하지\\s*마|쓰지\\s*말|금지)")
	correction = regexp.MustCompile(`(?i)하지\s*마|하지\s*말|지\s*마|지\s*말고|금지|하라고|하라니까|몇\s*번을|don'?t|do not|never|stop`)
)

// statements are the rule sentences, by action, in Korean and English.
var statements = map[string][2]string{
	alignment.ActPR:      {"PR을 만들지 않는다", "Do not open pull requests"},
	alignment.ActPush:    {"푸시하지 않는다", "Do not push"},
	alignment.ActRelease: {"릴리즈를 만들지 않는다", "Do not cut releases"},
	alignment.ActTag:     {"태그를 만들지 않는다", "Do not create tags"},
	alignment.ActDeploy:  {"배포하지 않는다", "Do not deploy"},
	alignment.ActCommit:  {"커밋하지 않는다", "Do not commit"},
	alignment.ActDelete:  {"파일을 지우지 않는다", "Do not delete files"},
	alignment.ActSend:    {"메시지를 보내지 않는다", "Do not send messages"},
	ActFullSuite:         {"전체 시험을 돌리지 않는다", "Do not run the whole test suite"},
	ActTestAfterDocs:     {"문서만 바뀐 뒤에는 시험을 돌리지 않는다", "Do not run tests after a documentation-only change"},
	ActNewBranch:         {"새 브랜치를 만들지 않고 현재 브랜치에 커밋한다", "Commit on the current branch; do not create a new one"},
	ActKoreanAnswer:      {"한국어로 답한다", "Answer in Korean"},
	ActAvoidPath:         {"`%s`를 쓰지 않는다", "Do not use `%s`"},
}

// Statement is the rule sentence of c in the given language ("en" or
// Korean otherwise).
func Statement(c Correction, language string) string {
	s := statements[c.Action]
	text := s[0]
	if language == "en" {
		text = s[1]
	}
	if c.Kind == KindRequire {
		text = requireText(c.Action, language)
	}
	return strings.Replace(text, "%s", c.Target, 1)
}

func requireText(act, language string) string {
	ko := map[string]string{alignment.ActRelease: "요청한 작업은 릴리즈까지 한다", alignment.ActPush: "요청한 작업은 푸시까지 한다", alignment.ActCommit: "요청한 작업은 커밋까지 한다", alignment.ActDeploy: "요청한 작업은 배포까지 한다"}
	en := map[string]string{alignment.ActRelease: "Finish requested work with a release", alignment.ActPush: "Finish requested work with a push", alignment.ActCommit: "Finish requested work with a commit", alignment.ActDeploy: "Finish requested work with a deploy"}
	if language == "en" {
		return en[act]
	}
	return ko[act]
}

var requireAsk = regexp.MustCompile(`(?i)(릴리즈|릴리스|푸시|커밋|배포)\s*까지\s*(해|하라|하라고|하라니까|해야)`)

// Extract reads the corrections in one user message. A message that only
// asks for something is not a correction; the words have to correct
// ("하지 마", "하라고", "몇 번을").
func Extract(msg string) []Correction {
	if utf8.RuneCountInString(msg) >= 2000 || !correction.MatchString(msg) {
		return nil
	}
	persistent := lasting.MatchString(msg)
	var out []Correction
	add := func(kind, act, target string) {
		c := Correction{Kind: kind, Action: act, Target: target, Persistent: persistent}
		c.Statement = Statement(c, "ko")
		for _, x := range out {
			if x.Kind == c.Kind && x.Action == c.Action && x.Target == c.Target {
				return
			}
		}
		out = append(out, c)
	}
	for _, d := range alignment.Deliverables(msg, 0) {
		if d.Forbidden {
			add(KindForbid, d.Action, "")
		}
	}
	if m := requireAsk.FindStringSubmatch(msg); m != nil {
		act := map[string]string{"릴리즈": alignment.ActRelease, "릴리스": alignment.ActRelease, "푸시": alignment.ActPush, "커밋": alignment.ActCommit, "배포": alignment.ActDeploy}[m[1]]
		add(KindRequire, act, "")
	}
	if fullSuite.MatchString(msg) {
		add(KindForbid, ActFullSuite, "")
	}
	if afterDocs.MatchString(msg) {
		add(KindForbid, ActTestAfterDocs, "")
	}
	if newBranch.MatchString(msg) {
		add(KindForbid, ActNewBranch, "")
	}
	if korean.MatchString(msg) {
		add(KindWriting, ActKoreanAnswer, "")
	}
	for _, m := range avoidPath.FindAllStringSubmatch(msg, 4) {
		p := m[1]
		if p == "" {
			p = m[2]
		}
		if strings.Contains(p, "/") {
			add(KindResource, ActAvoidPath, p)
		}
	}
	return out
}

// ID identifies a rule within a project, whatever message gave it.
func ID(project string, c Correction) string {
	return "corr_" + fp.Hash(project, c.Kind, c.Action, c.Target)[:12]
}

var newBranchCmd = regexp.MustCompile(`\bgit\s+(checkout\s+-b|switch\s+-c|switch\s+--create|branch\s+[^-\s]\S*|worktree\s+add\s+-b)`)

// Call is what the check points know about a tool call.
type Call struct {
	Cmd   string
	Paths []string
	// Full: the call runs the whole test suite. Verify: it runs a check.
	// DocsOnly: everything changed since the last passing check is docs.
	Full, Verify, DocsOnly bool
	// Requested: the current request asks for this action itself, which
	// wins over a rule.
	Requested func(action string) bool
}

// Breaks reports whether a call breaks rule c.
func Breaks(c Correction, call Call) bool {
	if c.Kind != KindForbid && c.Kind != KindResource {
		return false
	}
	if call.Requested != nil && call.Requested(c.Action) {
		return false
	}
	switch c.Action {
	case ActFullSuite:
		return call.Full
	case ActTestAfterDocs:
		return call.Verify && call.DocsOnly
	case ActNewBranch:
		return newBranchCmd.MatchString(call.Cmd)
	case ActAvoidPath:
		if strings.Contains(call.Cmd, c.Target) {
			return true
		}
		for _, p := range call.Paths {
			if p == c.Target || strings.HasPrefix(p, strings.TrimSuffix(c.Target, "/")+"/") {
				return true
			}
		}
		return false
	}
	for _, x := range alignment.Classify(call.Cmd, nil) {
		if x.Action == c.Action && !x.Wrapper {
			return true
		}
	}
	return false
}
