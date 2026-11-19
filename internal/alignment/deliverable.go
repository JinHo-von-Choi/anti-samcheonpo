package alignment

import (
	"path/filepath"
	"regexp"
	"strings"
)

// Deliverable actions: the outward results a user asks for. Each one has a
// command that shows it happened.
const (
	ActRelease = "release"
	ActTag     = "tag"
	ActPush    = "push"
	ActCommit  = "commit"
	ActDeploy  = "deploy"
	ActDelete  = "delete"
	ActPR      = "pr"
	ActSend    = "send"
	// ActIssue and ActReview are outward actions nobody asked for in the
	// cases this ledger watches; they are only ever unrequested.
	ActIssue = "issue"
)

// Deliverable states.
const (
	StateMissing      = "missing"
	StateMet          = "met"
	StateWithdrawn    = "withdrawn"
	StateUnobservable = "unobservable"
)

// Deliverable is one outward result a user message asked for or forbade.
type Deliverable struct {
	Action string `json:"action"`
	// Target is what the action is about: a version, a branch, a path. It
	// is empty when the message named none.
	Target string `json:"target,omitempty"`
	// Conditional is set when the message makes the action depend on
	// something ("테스트 통과하면 릴리즈"): it is not owed unconditionally.
	Conditional bool `json:"conditional,omitempty"`
	// Forbidden is set for an explicit negation ("푸시는 하지 마").
	Forbidden bool   `json:"forbidden,omitempty"`
	State     string `json:"state"`
	// Other is a different target a command of the same action named
	// ("v0.12.0 was released, v0.13.0 was asked for").
	Other string `json:"other,omitempty"`
	// Seq is the request the deliverable came from.
	Seq int64 `json:"seq"`
}

type deliverableWord struct {
	act string
	// ask is the imperative form: a noun alone ("릴리즈 노트") is not an ask.
	ask *regexp.Regexp
	// no is the explicit negation of the action.
	no *regexp.Regexp
}

var deliverableWords = []deliverableWord{
	{ActRelease, regexp.MustCompile(`(?i)(릴리즈|릴리스)\s*(까지|해|하고|하자|만들|내|올려|진행)|(cut|make|create|publish|do)\s+(a\s+|the\s+)?release|release\s+(it|v?\d)`),
		regexp.MustCompile(`(?i)(릴리즈|릴리스)\S*\s*(하지\s*마|하지\s*말|말고|금지|안\s*해도|안\s*해|는\s*빼|는\s*나중)|(don'?t|do not|never|no)\s+(cut\s+a\s+)?release`)},
	{ActTag, regexp.MustCompile(`(?i)태그\s*(를\s*)?(달|찍|만들|붙여|생성)|(create|add|push)\s+(a\s+|the\s+)?tag`),
		regexp.MustCompile(`(?i)태그\S*\s*(달지\s*마|찍지\s*마|만들지\s*마|하지\s*마|말고|금지)|(don'?t|do not|never|no)\s+tag`)},
	{ActPush, regexp.MustCompile(`(?i)(푸시|푸쉬)\s*(까지|해|하고|하자|진행)|push\s+(it|this|the|to|origin|them)|\bgit\s+push\b`),
		regexp.MustCompile(`(?i)(푸시|푸쉬)\S*\s*(하지\s*마|하지\s*말|말고|금지|안\s*해도|안\s*해|는\s*빼|는\s*나중)|(don'?t|do not|never|no)\s+push`)},
	{ActCommit, regexp.MustCompile(`(?i)커밋\s*(까지|만\s*해|해|하고|하자|진행)|commit\s+(it|this|the|them|changes)`),
		regexp.MustCompile(`(?i)커밋\S*\s*(하지\s*마|하지\s*말|말고|금지|안\s*해도|안\s*해)|(don'?t|do not|never|no)\s+commit`)},
	{ActDeploy, regexp.MustCompile(`(?i)배포\s*(까지|해|하고|하자|진행)|deploy\s+(it|this|the|to)`),
		regexp.MustCompile(`(?i)배포\S*\s*(하지\s*마|하지\s*말|말고|금지|안\s*해도|안\s*해|는\s*빼|는\s*나중)|(don'?t|do not|never|no)\s+deploy`)},
	{ActDelete, regexp.MustCompile(`(?i)(지워|지우고|지워라|삭제\s*해|삭제하고|제거\s*해|제거하고)|(delete|remove)\s+(the\s+)?\S`),
		regexp.MustCompile(`(?i)(지우지\s*마|삭제하지\s*마|제거하지\s*마|지우지\s*말|삭제하지\s*말)|(don'?t|do not|never)\s+(delete|remove)`)},
	{ActPR, regexp.MustCompile(`(?i)(PR|피알|풀\s*리퀘스트)\s*(을|를)?\s*(까지|해|만들|올려|생성|열어)|(open|create|make|submit)\s+(a\s+)?(PR|pull request)`),
		regexp.MustCompile(`(?i)(PR|피알|풀\s*리퀘스트)\S*\s*(만들지\s*마|올리지\s*마|하지\s*마|하지\s*말|말고|금지)|(don'?t|do not|never|no)\s+(open\s+|create\s+)?(a\s+)?(PR|pull request)`)},
	{ActSend, regexp.MustCompile(`(?i)(메일|메시지|문자|슬랙|알림)\S*\s*(보내|전송)|send\s+(an?\s+)?(email|message|mail)`),
		regexp.MustCompile(`(?i)(메일|메시지|문자|슬랙|알림)\S*\s*(보내지\s*마|전송하지\s*마|말고)|(don'?t|do not|never)\s+send`)},
}

var (
	clauseSplit = regexp.MustCompile(`[.!]+(?:\s+|$)|[\n;]+|,\s*|그리고\s|\band\s+then\b`)
	question    = regexp.MustCompile(`(?i)\?|할까|될까|되겠어|되나|어때|해도\s*(돼|될)|가능해|있어\s*$|\bshould i\b|\bcan i\b`)
	condition   = regexp.MustCompile(`(?i)(통과|성공|끝나|완료되|확인되|괜찮|문제\s*없)\S*\s*(면|으면)\s|되면\s|하면\s|\bif\b|\bonce\b|\bafter\b`)
	versionRe   = regexp.MustCompile(`\bv?\d+\.\d+\.\d+(?:-[0-9A-Za-z.]+)?\b`)
	pathRe      = regexp.MustCompile("`([^`\\s]+)`|(?:^|\\s)((?:\\.{0,2}/)?[\\w.-]+/[\\w./-]+|[\\w-]+\\.[A-Za-z0-9]{1,6})(?:\\s|$|을|를|은|는|도)")
)

// Deliverables reads the outward actions one user message asks for or
// forbids. Questions ("PR해도 되겠어?") ask for nothing; a negation is a
// prohibition, and a condition makes the action conditional. An action the
// message does not mention is neither asked for nor forbidden.
func Deliverables(msg string, seq int64) []Deliverable {
	if len(msg) > 4000 {
		return nil
	}
	var out []Deliverable
	seen := map[string]bool{}
	for _, clause := range clauseSplit.Split(msg, -1) {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		for _, w := range deliverableWords {
			forbid := w.no.MatchString(clause)
			if !forbid && (!w.ask.MatchString(clause) || question.MatchString(clause)) {
				continue
			}
			d := Deliverable{Action: w.act, Forbidden: forbid, Seq: seq, State: StateMissing}
			if !forbid {
				d.Conditional = condition.MatchString(clause)
			}
			switch w.act {
			case ActRelease, ActTag:
				d.Target = versionRe.FindString(clause)
			case ActDelete:
				d.Target = firstPath(clause)
				if d.Target == "" && !forbid {
					// "그 부분 지워" removes content, not a file a
					// command would show
					d.State = StateUnobservable
				}
			case ActSend:
				// mail and chat go through tools, not the shell
				d.State = StateUnobservable
			}
			key := d.Action + "|" + d.Target + "|" + boolKey(d.Forbidden)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, d)
		}
	}
	return out
}

func boolKey(b bool) string {
	if b {
		return "no"
	}
	return "yes"
}

func firstPath(s string) string {
	for _, m := range pathRe.FindAllStringSubmatch(s, -1) {
		for _, g := range m[1:] {
			if g != "" {
				return strings.Trim(g, "'\"")
			}
		}
	}
	return ""
}

// Book is a session's deliverables: what the user asked for, what was
// forbidden, and what the commands showed.
type Book struct {
	Items []Deliverable `json:"items"`
	// Deploys are the project's own deploy commands. Without them a deploy
	// cannot be told from the commands, and an asked deploy is
	// unobservable unless a deploy-like command runs.
	Deploys []string `json:"-"`
	// tagged is set once a tag was made: a push after it may start a
	// release workflow that cuts the release outside the session.
	tagged bool
}

// Add folds one request's deliverables in. A later ask lifts an earlier
// prohibition of the same action, and a later prohibition withdraws an
// earlier ask: the current request wins.
func (b *Book) Add(ds []Deliverable) {
	for _, d := range ds {
		for i := range b.Items {
			it := &b.Items[i]
			if it.Action != d.Action || it.State == StateWithdrawn {
				continue
			}
			if it.Forbidden != d.Forbidden && (d.Target == "" || it.Target == "" || d.Target == it.Target) {
				it.State = StateWithdrawn
			}
		}
		if d.Action == ActDeploy && !d.Forbidden && len(b.Deploys) == 0 {
			d.State = StateUnobservable
		}
		b.Items = append(b.Items, d)
	}
}

// Forbidden returns the live prohibition of act, if any.
func (b *Book) Forbidden(act string) *Deliverable {
	for i := len(b.Items) - 1; i >= 0; i-- {
		it := &b.Items[i]
		if it.Action == act && it.Forbidden && it.State != StateWithdrawn {
			return it
		}
	}
	return nil
}

// Requested reports whether the session asked for act at all.
func (b *Book) Requested(act string) bool {
	for _, it := range b.Items {
		if it.Action == act && !it.Forbidden && it.State != StateWithdrawn {
			return true
		}
	}
	return false
}

// Owed returns the asks that are unconditional and not yet met.
func (b *Book) Owed() []Deliverable {
	var out []Deliverable
	for _, it := range b.Items {
		if !it.Forbidden && !it.Conditional && it.State != StateWithdrawn && it.State != StateMet {
			out = append(out, it)
		}
	}
	return out
}

// Command is what a successful shell command did, as far as the book can
// tell.
type Command struct {
	Action string
	// Target is the tag, branch or path the command named.
	Target string
	// Wrapper is set for a command that mentions the action without being
	// the command that shows it (make release, ./deploy.sh): the result is
	// unobservable, not missing.
	Wrapper bool
}

var (
	ghRelease = regexp.MustCompile(`\bgh\s+release\s+create\s+(\S+)`)
	gitTag    = regexp.MustCompile(`\bgit\s+tag\s+(?:-[a-z]\s+)*(?:-m\s+\S+\s+)?([^\s-]\S*)`)
	gitPush   = regexp.MustCompile(`\bgit\s+push\b`)
	gitCommit = regexp.MustCompile(`\bgit\s+commit\b`)
	ghPR      = regexp.MustCompile(`\bgh\s+pr\s+create\b`)
	ghIssue   = regexp.MustCompile(`\bgh\s+issue\s+(create|comment)\b|\bgh\s+pr\s+(comment|review)\b`)
	rmCmd     = regexp.MustCompile(`(?:^|[;&|]\s*)(?:git\s+rm|rm|trash|trash-put)\s+(?:-\S+\s+)*(.+)`)
	wrapper   = map[string]*regexp.Regexp{
		ActRelease: regexp.MustCompile(`(?i)\b(make|just|task|npm run|pnpm|yarn|bash|sh|\./)\S*\s*\S*release|goreleaser|semantic-release`),
		ActDeploy:  regexp.MustCompile(`(?i)\b(make|just|task|npm run|pnpm|yarn|bash|sh|\./)\S*\s*\S*deploy|kubectl\s+apply|helm\s+(upgrade|install)|terraform\s+apply|flyctl\s+deploy|vercel\b|wrangler\s+deploy`),
		ActPush:    regexp.MustCompile(`(?i)\b(make|just|task|npm run)\s+\S*(push|publish)`),
		ActSend:    regexp.MustCompile(`(?i)\b(sendmail|mail|curl\s+.*(slack|hooks|webhook|telegram))`),
	}
)

// Classify reads the outward actions a shell command performs. deploy lists
// the project's own deploy commands.
func Classify(cmd string, deploy []string) []Command {
	var out []Command
	if m := ghRelease.FindStringSubmatch(cmd); m != nil {
		out = append(out, Command{Action: ActRelease, Target: m[1]}, Command{Action: ActTag, Target: m[1]})
	}
	if m := gitTag.FindStringSubmatch(cmd); m != nil && !strings.Contains(cmd, " -d ") && !strings.Contains(cmd, " -l") {
		out = append(out, Command{Action: ActTag, Target: m[1]})
	}
	if gitPush.MatchString(cmd) && !strings.Contains(cmd, "--dry-run") {
		out = append(out, Command{Action: ActPush})
	}
	if gitCommit.MatchString(cmd) {
		out = append(out, Command{Action: ActCommit})
	}
	if ghPR.MatchString(cmd) {
		out = append(out, Command{Action: ActPR})
	}
	if ghIssue.MatchString(cmd) {
		out = append(out, Command{Action: ActIssue})
	}
	if m := rmCmd.FindStringSubmatch(cmd); m != nil {
		for _, f := range strings.Fields(m[1]) {
			if strings.ContainsAny(f, ";&|") {
				break
			}
			out = append(out, Command{Action: ActDelete, Target: strings.Trim(f, `'"`)})
		}
	}
	for _, d := range deploy {
		if d = strings.TrimSpace(d); d != "" && strings.Contains(cmd, d) {
			out = append(out, Command{Action: ActDeploy})
		}
	}
	for act, re := range wrapper {
		if re.MatchString(cmd) && !hasAction(out, act) {
			out = append(out, Command{Action: act, Wrapper: true})
		}
	}
	return out
}

func hasAction(cs []Command, act string) bool {
	for _, c := range cs {
		if c.Action == act {
			return true
		}
	}
	return false
}

// Observe marks the asks a successful command met. A wrapper leaves an ask
// unobservable; a met ask stays met.
func (b *Book) Observe(cs []Command) {
	b.tagged = b.tagged || hasAction(cs, ActTag)
	if b.tagged && hasAction(cs, ActPush) {
		for i := range b.Items {
			if it := &b.Items[i]; it.Action == ActRelease && !it.Forbidden && it.State == StateMissing {
				it.State = StateUnobservable
			}
		}
	}
	for _, c := range cs {
		for i := range b.Items {
			it := &b.Items[i]
			if it.Forbidden || it.State == StateWithdrawn || it.State == StateMet || it.Action != c.Action {
				continue
			}
			if c.Wrapper {
				it.State = StateUnobservable
				continue
			}
			if it.Target != "" && c.Target != "" && !sameTarget(it.Action, it.Target, c.Target) {
				it.Other = c.Target
				continue
			}
			it.State = StateMet
		}
	}
}

// sameTarget compares a requested target with a command's.
func sameTarget(act, want, got string) bool {
	if act == ActDelete {
		return filepath.Clean(want) == filepath.Clean(got) || strings.HasSuffix(filepath.Clean(got), "/"+filepath.Clean(want)) || strings.HasSuffix(filepath.Clean(want), "/"+filepath.Clean(got))
	}
	return strings.TrimPrefix(want, "v") == strings.TrimPrefix(got, "v")
}
