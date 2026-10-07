package live

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/intent"
)

func (s *Session) restoreIntent() error {
	if s.db == nil {
		return nil
	}
	task, revision, link, err := s.db.SessionIntent(s.Agent, s.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if task.ProjectID != fp.Hash("project", filepath.Clean(s.Root)) {
		return errors.New("session intent belongs to another project")
	}
	s.task, s.intentRevision, s.taskLink = &task, &revision, &link
	s.parser.Session.TaskID, s.parser.Session.TaskRevision = task.ID, revision.Number
	s.firstPrompt = revision.Goal
	return nil
}

func intentSource(s *Session, ev *event.Event) intent.Source {
	return intent.Source{Origin: intent.User, SessionID: s.ID, EventID: fmt.Sprintf("event:%d:%s", ev.Seq, ev.TS.UTC().Format(time.RFC3339Nano)), At: ev.TS}
}

// recordIntent runs under s.mu, after any necessary authority revocation.
// A revision is an attributable interpretation, never shell authorization.
func (s *Session) recordIntent(change intent.Change, source intent.Source) error {
	if change.Kind == intent.Ignored {
		return nil
	}
	if s.db == nil {
		return errors.New("intent ledger unavailable")
	}
	var task intent.Task
	var err error
	if s.task == nil {
		task, err = intent.NewTask(fp.Hash("project", filepath.Clean(s.Root)), source.At)
		if err != nil {
			return err
		}
	} else {
		task = *s.task
	}
	goal := change.Goal
	if change.Kind == intent.Followup {
		if s.intentRevision != nil {
			goal = s.intentRevision.Goal
		} else if s.c != nil {
			goal = s.c.Goal
		}
	}
	r, err := intent.Revise(task.ID, s.intentRevision, goal, source)
	if err != nil {
		return err
	}
	r.Kind, r.Message = change.Kind, change.Goal
	if s.c != nil {
		r.ContractHash = s.c.ChecksHash()
	}
	link := intent.SessionLink{TaskID: task.ID, Agent: s.Agent, SessionID: s.ID}
	if s.taskLink != nil {
		link = *s.taskLink
	}
	if err = s.db.SaveTaskRevision(task, r, link); err != nil {
		return err
	}
	same := s.task != nil && s.intentRevision != nil && s.task.ID == task.ID && s.intentRevision.Number == r.Number
	s.task, s.intentRevision, s.taskLink = &task, &r, &link
	if !same {
		s.loadFailedAttempts()
	}
	// Evidence is bound to an intent revision, including attributable followups.
	// Do not keep a green completion count from the previous revision.
	s.checks = map[string]CheckResult{}
	s.parser.Session.TaskID, s.parser.Session.TaskRevision = task.ID, r.Number
	if change.Kind != intent.Followup {
		s.firstPrompt = r.Goal
		if s.eng != nil {
			s.eng.Retarget(r.Goal)
		}
	}
	return nil
}

func (s *Session) recordIntentCommand(change intent.Change) error {
	ev := &event.Event{Kind: event.KindPrompt, TS: time.Now(), Summary: "explicit intent command", Text: change.Goal, Basis: "live"}
	s.parser.AddEvent(ev)
	s.observe(ev)
	return s.recordIntent(change, intentSource(s, ev))
}

func (s *Session) goalCard(requested string) intent.Card {
	card := intent.GoalCard(s.c, s.acc, requested, nil)
	if r := s.intentRevision; r != nil {
		if card.Goal == "" {
			card.Goal = r.Goal
		}
		card.TaskID, card.Revision, card.Source = r.TaskID, r.Number, &r.Source
		card.LatestMessage = r.Message
	}
	return card
}
