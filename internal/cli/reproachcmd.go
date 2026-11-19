package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/alignment"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/classify"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/contract"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/correction"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/cost"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/detect"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/eval"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/lang"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/ledger"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/live"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/receipt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/reproach"
)

// transcriptFile is a host transcript to replay.
type transcriptFile struct {
	agent, path, session string
}

func transcripts(since time.Time) []transcriptFile {
	var out []transcriptFile
	// oldest first: a correction is compared with the ones before it
	defer func() {
		sort.SliceStable(out, func(i, j int) bool { return modTime(out[i].path).Before(modTime(out[j].path)) })
	}()
	add := func(agent, pattern string, id func(string) string) {
		paths, _ := filepath.Glob(pattern)
		for _, p := range paths {
			if st, err := os.Stat(p); err == nil && !st.ModTime().Before(since) {
				out = append(out, transcriptFile{agent: agent, path: p, session: id(p)})
			}
		}
	}
	add("claude", filepath.Join(claudeDir(), "*", "*.jsonl"), func(p string) string { return strings.TrimSuffix(filepath.Base(p), ".jsonl") })
	add("codex", filepath.Join(codexDir(), "*", "*", "*", "*.jsonl"), reproach.CodexSessionID)
	return out
}

func readMessages(f transcriptFile) ([]reproach.Message, error) {
	fh, err := os.Open(f.path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	if f.agent == "codex" {
		return reproach.ReadCodex(fh), nil
	}
	return reproach.ReadClaude(fh), nil
}

// userPrompts returns the ledger's user request events in order.
func userPrompts(events []*event.Event) []*event.Event {
	var out []*event.Event
	for _, ev := range events {
		if ev.Kind == event.KindPrompt && !ev.Forced && ev.PromptOrigin != "harness" {
			out = append(out, ev)
		}
	}
	return out
}

// promptFor finds the ledger request a transcript message became: the first
// request from index i on that the host reported after the message was
// written. A message typed while the agent works is delivered when the turn
// ends, so the report can come hours later, never earlier.
func promptFor(prompts []*event.Event, i int, ts time.Time) int {
	for j := i; j < len(prompts); j++ {
		if prompts[j].TS.Before(ts.Add(-10 * time.Second)) {
			continue
		}
		if prompts[j].TS.After(ts.Add(6 * time.Hour)) {
			return -1
		}
		return j
	}
	return -1
}

// replayReproaches reads host transcripts changed since and records their
// reproaches as a live session would. Sessions the live daemon already
// recorded are left as they are.
func replayReproaches(db *ledger.DB, since time.Time, tally *outcomeTally) (files, sessions int, err error) {
	for _, f := range transcripts(since) {
		files++
		msgs, err := readMessages(f)
		if err != nil || len(msgs) == 0 {
			continue
		}
		tally.add(f, msgs)
		basis, err := db.ReproachBasis(f.session)
		if err != nil {
			return files, sessions, err
		}
		if basis == "live" {
			// the live record is kept
			continue
		}
		if err := db.ClearReplay(f.session); err != nil {
			return files, sessions, err
		}
		started, linked, err := db.SessionStart(f.session)
		if err != nil {
			return files, sessions, err
		}
		if started.IsZero() {
			started = msgs[0].TS
		}
		var events []*event.Event
		var verdicts []detect.Signal
		var prompts []*event.Event
		if linked {
			if events, err = db.EventsOf(f.session); err != nil {
				return files, sessions, err
			}
			if verdicts, err = db.VerdictsOf(f.session); err != nil {
				return files, sessions, err
			}
			prompts = userPrompts(events)
		}
		at, typed := 0, 0
		for i, m := range msgs {
			j := -1
			if linked {
				if j = promptFor(prompts, at, m.TS); j >= 0 {
					at = j + 1
				}
			}
			if m.Command {
				continue
			}
			typed++
			r := reproach.Detect(m.Text)
			if !r.Scolding {
				continue
			}
			rec := ledger.Reproach{SessionID: f.session, Agent: f.agent, Themes: r.Themes, TextHash: r.Hash, CreatedAt: m.TS,
				DoneClaimed: detect.IsCompletionClaim(m.Before), Basis: ledger.BasisUnlinked, Seq: -int64(i + 1)}
			if j >= 0 {
				var start, next int64
				if j > 0 {
					start = prompts[j-1].Seq
				}
				if j+1 < len(prompts) {
					next = prompts[j+1].Seq
				}
				span := live.SpanReproach(start, prompts[j].Seq, next, r.Themes, verdicts, events)
				span.SessionID, span.Agent, span.TextHash, span.CreatedAt, span.DoneClaimed = f.session, f.agent, r.Hash, m.TS, rec.DoneClaimed
				span.Seq, span.StartSeq, span.Basis = prompts[j].Seq, start, "replay"
				rec = span
			}
			if err := db.SaveReproach(rec); err != nil {
				return files, sessions, err
			}
		}
		if err := db.SaveReproachSession(f.session, f.agent, "replay", typed, typed > 0, started); err != nil {
			return files, sessions, err
		}
		sessions++
	}
	return files, sessions, nil
}

func reproachCmds() []*cobra.Command {
	var days int
	var outcomeOnly bool
	replay := &cobra.Command{Use: "reproach-replay", Short: lang.T("지난 대화 기록의 질책을 다시 읽어 원장 판정과 맞춘다 (원문은 저장하지 않음)"), Args: cobra.NoArgs, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			db, err := openDB()
			if err != nil {
				return err
			}
			defer db.Close()
			since := time.Now().AddDate(0, 0, -max(1, days))
			tally := newOutcomeTally()
			if outcomeOnly {
				for _, f := range transcripts(since) {
					if msgs, err := readMessages(f); err == nil {
						tally.add(f, msgs)
					}
				}
				tally.print(cmd)
				return printKnownReruns(cmd, since)
			}
			files, sessions, err := replayReproaches(db, since, tally)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), lang.T("대화 기록 %d개를 읽어 %d개 세션의 질책을 기록했다\n"), files, sessions)
			rep, err := db.ReproachSummary(since)
			if err != nil {
				return err
			}
			printReproach(cmd, rep)
			tally.print(cmd)
			return printKnownReruns(cmd, since)
		}}
	replay.Flags().IntVar(&days, "days", 30, lang.T("읽을 기간(일)"))
	replay.Flags().BoolVar(&outcomeOnly, "outcome-only", false, lang.T("원장에 쓰지 않고 결과물·보고 규칙만 재생한다"))

	var labelDays int
	var labelOut string
	labels := &cobra.Command{Use: "reproach-labels", Short: lang.T("질책과 주제가 맞은 판정 구간을 user_reproach 등급 라벨로 내보낸다"), Args: cobra.NoArgs, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if labelOut == "" {
				return errors.New(lang.T("--out 디렉터리가 필요하다"))
			}
			db, err := openDB()
			if err != nil {
				return err
			}
			defer db.Close()
			n, err := writeReproachLabels(db, time.Now().AddDate(0, 0, -max(1, labelDays)), labelOut)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), lang.T("라벨 %d개를 썼다: %s\n"), n, labelOut)
			return nil
		}}
	labels.Flags().IntVar(&labelDays, "days", 30, lang.T("기간(일)"))
	labels.Flags().StringVar(&labelOut, "out", "", lang.T("라벨 디렉터리"))

	var sampleDays, sampleN int
	sample := &cobra.Command{Use: "reproach-sample", Short: lang.T("질책 감지 정밀도를 사람이 확인하도록 감지된·감지되지 않은 메시지 표본을 화면에만 출력한다"), Args: cobra.NoArgs, SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return printReproachSample(cmd, time.Now().AddDate(0, 0, -max(1, sampleDays)), sampleN)
		}}
	sample.Flags().IntVar(&sampleDays, "days", 30, lang.T("기간(일)"))
	sample.Flags().IntVar(&sampleN, "n", 100, lang.T("감지된 것과 감지되지 않은 것 각각의 표본 수"))
	return []*cobra.Command{replay, labels, sample}
}

// writeReproachLabels writes one label per verdict whose rule a reproach
// theme matched, over the scolded span.
func writeReproachLabels(db *ledger.DB, since time.Time, dir string) (int, error) {
	rows, err := db.Query(`SELECT session_id FROM reproach_session WHERE started_at>=? AND observed=1`, since.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		list, err := db.SessionReproaches(id)
		if err != nil {
			return n, err
		}
		var out []eval.Label
		for _, r := range list {
			if r.Basis == ledger.BasisUnlinked {
				continue
			}
			for _, rule := range r.Matched {
				out = append(out, eval.Label{Session: id, Start: r.StartSeq + 1, End: r.Seq - 1, Symptom: strings.ToUpper(strings.SplitN(rule, ".", 2)[0]),
					Justified: "yes", Labeler: "user_reproach", Source: eval.LabelSourceReproach, Note: rule,
					Derivation: &eval.Derivation{Method: eval.DerivationReproach, Digest: r.TextHash}})
			}
		}
		if len(out) == 0 {
			continue
		}
		b, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return n, err
		}
		if err := os.WriteFile(filepath.Join(dir, id+".json"), append(b, '\n'), 0o600); err != nil {
			return n, err
		}
		n += len(out)
	}
	return n, nil
}

// printReproachSample prints random detected and undetected user messages
// for a person to read. Nothing is written to disk.
func printReproachSample(cmd *cobra.Command, since time.Time, n int) error {
	var hit, miss []string
	for _, f := range transcripts(since) {
		msgs, err := readMessages(f)
		if err != nil {
			continue
		}
		for _, m := range msgs {
			r := reproach.Detect(m.Text)
			line := strings.Join(strings.Fields(m.Text), " ")
			if len([]rune(line)) > 200 {
				line = string([]rune(line)[:200]) + "…"
			}
			if m.Command {
				continue
			}
			if r.Scolding {
				hit = append(hit, fmt.Sprintf("[%s] %s", strings.Join(r.Themes, ","), line))
			} else if len([]rune(m.Text)) < reproach.MaxRunes {
				miss = append(miss, line)
			}
		}
	}
	rng := rand.New(rand.NewPCG(uint64(len(hit)), uint64(len(miss))))
	pick := func(xs []string) []string {
		rng.Shuffle(len(xs), func(i, j int) { xs[i], xs[j] = xs[j], xs[i] })
		return xs[:min(n, len(xs))]
	}
	w := cmd.OutOrStdout()
	fmt.Fprintf(w, lang.T("감지된 메시지 %d건 중 %d건\n"), len(hit), min(n, len(hit)))
	for i, s := range pick(hit) {
		fmt.Fprintf(w, "D%03d %s\n", i+1, s)
	}
	fmt.Fprintf(w, lang.T("\n감지되지 않은 메시지 %d건 중 %d건\n"), len(miss), min(n, len(miss)))
	for i, s := range pick(miss) {
		fmt.Fprintf(w, "N%03d %s\n", i+1, s)
	}
	return nil
}

// printReproach shows the reproach rate, its themes and the themes no rule
// matched.
func printReproach(cmd *cobra.Command, rep *ledger.ReproachReport) {
	w := cmd.OutOrStdout()
	if rep == nil || rep.Sessions == 0 {
		fmt.Fprintln(w, lang.T("질책: 사용자 입력을 읽은 세션 없음 (미관측)"))
		return
	}
	fmt.Fprintf(w, lang.T("질책: 사용자 메시지 %d건 중 %d건 (%.1f%%), 세션 %d개, 입력 없는 세션 %d개, 원장에 없는 세션의 질책 %d건\n"),
		rep.UserMessages, rep.Reproaches, 100*rep.Rate(), rep.Sessions, rep.Unobserved, rep.Unlinked)
	fmt.Fprintf(w, lang.T("직전 답이 완료를 말한 질책 %d건, 질책 직전 비용 %s(토큰 %d), 재작업 비용 %s(토큰 %d)\n"),
		rep.DoneClaimed, wonMicro(rep.SpanMicro), rep.SpanTokens, wonMicro(rep.ReworkMicro), rep.ReworkTok)
	fmt.Fprintln(w, lang.T("에이전트별 질책:"), countList(rep.ByAgent))
	fmt.Fprintln(w, lang.T("주제:"), countList(rep.Themes))
	fmt.Fprintln(w, lang.T("맞는 규칙이 울리지 않은 주제:"), countList(rep.Missed))
	if len(rep.Weeks) > 0 {
		var parts []string
		for _, wk := range rep.Weeks {
			rate := "-"
			if wk.UserMessages > 0 {
				rate = fmt.Sprintf("%.1f%%", 100*float64(wk.Reproaches)/float64(wk.UserMessages))
			}
			parts = append(parts, wk.Week+" "+rate)
		}
		fmt.Fprintln(w, lang.T("주별 질책률:"), strings.Join(parts, ", "))
	}
}

// wonMicro writes micro-won as won; moneyOutput converts it for a dollar
// locale.
func wonMicro(micro int64) string { return contract.Comma(cost.Won(micro)) + lang.T("원") }

func countList(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return m[keys[i]] > m[keys[j]] || m[keys[i]] == m[keys[j]] && keys[i] < keys[j] })
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s %d", k, m[k]))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

// storedOutcome rebuilds a session's outcome line from the ledger. A
// session whose reproaches were never read is not observed.
func storedOutcome(db *ledger.DB, session string) *receipt.Outcome {
	basis, err := db.ReproachBasis(session)
	if err != nil || basis == "" {
		return &receipt.Outcome{}
	}
	var observed int
	_ = db.QueryRow(`SELECT observed FROM reproach_session WHERE session_id=?`, session).Scan(&observed)
	o := &receipt.Outcome{Observed: observed == 1}
	list, err := db.SessionReproaches(session)
	if err != nil {
		return o
	}
	for _, r := range list {
		o.Reproaches++
		o.SpanMicro += r.SpanMicro
		o.SpanTokens += r.SpanTokens
		o.ReworkMicro += r.ReworkMicro
		o.ReworkTokens += r.ReworkTok
	}
	o.Priced = o.SpanTokens == 0 || o.SpanMicro > 0
	return o
}

// reproachNotices are the audit's reproach lines: the rate by week and the
// themes. Zero reproaches and no observation read differently.
func reproachNotices(rep *ledger.ReproachReport) []string {
	if rep.Sessions == 0 {
		return []string{lang.T("질책: 사용자 입력을 읽은 세션 없음 (미관측, `samcheonpo rules reproach-replay`로 지난 기록을 읽을 수 있다)")}
	}
	out := []string{fmt.Sprintf(lang.T("질책: 사용자 메시지 %d건 중 %d건 (%.1f%%), 입력 없는 세션 %d개"), rep.UserMessages, rep.Reproaches, 100*rep.Rate(), rep.Unobserved)}
	if len(rep.Weeks) > 0 {
		var parts []string
		for _, wk := range rep.Weeks {
			if wk.UserMessages > 0 {
				parts = append(parts, fmt.Sprintf("%s %.1f%%", wk.Week, 100*float64(wk.Reproaches)/float64(wk.UserMessages)))
			}
		}
		out = append(out, lang.T("주별 질책률: ")+strings.Join(parts, ", "))
	}
	if rep.Reproaches > 0 {
		out = append(out, lang.T("질책 주제: ")+countList(rep.Themes))
	}
	return out
}

// outcomeTally is what the deliverable and report rules would have judged
// over replayed request spans, split by whether the user scolded next.
type outcomeTally struct {
	Spans, Scolded int
	// Judged and JudgedScolded count spans per rule; JudgedNamed counts the
	// judged spans whose next message names a theme the rule matches,
	// scolding or not ("한국말로").
	Judged, JudgedScolded, JudgedNamed map[string]int
	// Themed counts scolded spans per theme; Caught counts those a rule
	// matching the theme judged.
	Themed, Caught map[string]int
	// corrSeen holds the first time each correction was given in a project.
	corrSeen map[string]*firstCorrection
	// Repeats are corrections given again; Candidate are those a rule
	// already existed for at the time (a lasting scope the first time);
	// Judged are those whose span before the repeat broke that rule;
	// Withdrawn are those the user had asked against in between.
	CorrRepeats, CorrCandidate, CorrJudged, CorrWithdrawn int
}

type firstCorrection struct {
	session    string
	persistent bool
	withdrawn  bool
	c          correction.Correction
}

func newOutcomeTally() *outcomeTally {
	return &outcomeTally{Judged: map[string]int{}, JudgedScolded: map[string]int{}, JudgedNamed: map[string]int{}, Themed: map[string]int{}, Caught: map[string]int{},
		corrSeen: map[string]*firstCorrection{}}
}

// add replays one transcript: span k runs from message k to message k+1,
// whose Shell and Before hold what the agent did and said in it.
func (t *outcomeTally) add(f transcriptFile, msgs []reproach.Message) {
	t.addCorrections(f, msgs)
	var book alignment.Book
	request := ""
	// a transcript carries no contract: a deploy is unobservable unless a
	// deploy-like command runs
	for k := 0; k+1 < len(msgs); k++ {
		if !msgs[k].Command {
			book.Add(alignment.Deliverables(msgs[k].Text, int64(k)))
			request = msgs[k].Text
		}
		next := msgs[k+1]
		fired := map[string]bool{}
		for _, sh := range next.Shell {
			for _, c := range alignment.Classify(sh.Cmd, nil) {
				if c.Wrapper {
					continue
				}
				if book.Forbidden(c.Action) != nil {
					fired[live.RuleForbiddenAction] = true
				} else if (c.Action == alignment.ActPR || c.Action == alignment.ActIssue) && !book.Requested(c.Action) {
					fired[live.RuleUnrequested] = true
				}
			}
			if sh.OK {
				book.Observe(alignment.Classify(sh.Cmd, nil))
			}
		}
		answer := next.Before
		if detect.IsCompletionClaim(answer) {
			for _, d := range book.Owed() {
				if d.State == alignment.StateMissing {
					fired[live.RuleDeliverableMissing] = true
				}
			}
		}
		if lang.Detect(request) == lang.Korean && !alignment.WantsEnglish(request) && alignment.EnglishAnswer(answer) {
			fired[live.RuleAnswerLanguage] = true
		}
		if next.Command {
			continue
		}
		t.Spans++
		r := reproach.Detect(next.Text)
		if r.Scolding {
			t.Scolded++
			for _, th := range r.Themes {
				t.Themed[th]++
				for rule := range fired {
					if reproach.Matches(th, rule) {
						t.Caught[th]++
						break
					}
				}
			}
		}
		named := reproach.ThemesOf(next.Text)
		for rule := range fired {
			t.Judged[rule]++
			switch {
			case r.Scolding:
				t.JudgedScolded[rule]++
			case slices.ContainsFunc(named, func(th string) bool { return reproach.Matches(th, rule) }):
				t.JudgedNamed[rule]++
			}
		}
	}
}

// addCorrections replays the corrections of one transcript against those
// given before in the same project. Transcripts are read oldest first.
func (t *outcomeTally) addCorrections(f transcriptFile, msgs []reproach.Message) {
	project := filepath.Base(filepath.Dir(f.path))
	for _, m := range msgs {
		if m.Command {
			continue
		}
		for _, d := range alignment.Deliverables(m.Text, 0) {
			if d.Forbidden {
				continue
			}
			for key, first := range t.corrSeen {
				if strings.HasPrefix(key, project+"|") && first.c.Action == d.Action {
					first.withdrawn = true
				}
			}
		}
		for _, c := range correction.Extract(m.Text) {
			key := project + "|" + c.Kind + "|" + c.Action + "|" + c.Target
			first := t.corrSeen[key]
			if first == nil {
				t.corrSeen[key] = &firstCorrection{session: f.session, persistent: c.Persistent, c: c}
				continue
			}
			t.CorrRepeats++
			if first.withdrawn {
				t.CorrWithdrawn++
				first.withdrawn = false
				continue
			}
			if !first.persistent && first.session == f.session {
				continue // still one session: no rule yet
			}
			t.CorrCandidate++
			if correctionBrokenIn(first.c, m) {
				t.CorrJudged++
			}
		}
	}
}

// correctionBrokenIn reports whether the span before message m broke c.
func correctionBrokenIn(c correction.Correction, m reproach.Message) bool {
	if c.Kind == correction.KindWriting {
		return c.Action == correction.ActKoreanAnswer && alignment.EnglishAnswer(m.Before)
	}
	for _, sh := range m.Shell {
		tests := classify.TestCommands(sh.Cmd)
		call := correction.Call{Cmd: sh.Cmd, Verify: len(tests) > 0}
		for _, x := range tests {
			call.Full = call.Full || classify.CommandScope(x) == classify.ScopeFull
		}
		if correction.Breaks(c, call) {
			return true
		}
	}
	return false
}

func (t *outcomeTally) print(cmd *cobra.Command) {
	w := cmd.OutOrStdout()
	quiet := t.Spans - t.Scolded
	fmt.Fprintf(w, lang.T("\n결과물·보고 규칙 재생: 요청 구간 %d개, 다음 메시지가 질책인 구간 %d개\n"), t.Spans, t.Scolded)
	for _, rule := range []string{live.RuleDeliverableMissing, live.RuleForbiddenAction, live.RuleUnrequested, live.RuleAnswerLanguage} {
		n, ns, nn := t.Judged[rule], t.JudgedScolded[rule], t.JudgedNamed[rule]
		burden := 0.0
		if quiet > 0 {
			burden = 100 * float64(n-ns-nn) / float64(quiet)
		}
		fmt.Fprintf(w, lang.T("%s: 판정 %d구간, 그중 질책이 이어진 구간 %d, 질책 없이 같은 문제를 짚은 구간 %d, 둘 다 없는 구간의 판정률 %.2f%%\n"), rule, n, ns, nn, burden)
	}
	fmt.Fprintf(w, lang.T("같은 교정이 다시 나온 경우 %d건: 그때 규칙 후보가 있던 경우 %d건, 그 후보가 직전 구간의 위반을 판정한 경우 %d건, 그사이 사용자가 반대로 요청한 경우 %d건\n"),
		t.CorrRepeats, t.CorrCandidate, t.CorrJudged, t.CorrWithdrawn)
	for _, th := range []string{"deliverable_missing", "delete_ignored", "false_done", "theater", "language"} {
		if t.Themed[th] > 0 {
			fmt.Fprintf(w, lang.T("주제 %s: 질책 %d구간 중 맞는 규칙이 판정한 구간 %d\n"), th, t.Themed[th], t.Caught[th])
		}
	}
}

func modTime(p string) time.Time {
	st, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

// printKnownReruns shows, per agent, the checks that ran again with the
// same command after passing, with nothing or only documentation written
// in between.
func printKnownReruns(cmd *cobra.Command, since time.Time) error {
	db, err := openDB()
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.KnownReruns(since, live.DocFile)
	if err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	fmt.Fprintln(w, lang.T("\n결과를 이미 아는 재실행 (같은 명령, 통과 뒤 변경 없음·문서만 변경):"))
	for _, r := range rows {
		fmt.Fprintf(w, lang.T("%s: 검사 %d회 중 변경 없음 %d회, 문서만 변경 %d회\n"), r.Agent, r.Checks, r.Unchanged, r.Docs)
	}
	return nil
}
