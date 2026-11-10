package live

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/event"
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
	if !intent.MatchesProject(task.ProjectID, s.Root) {
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
		task, err = intent.NewTask(intent.ProjectID(s.Root), source.At)
		if err != nil {
			return err
		}
	} else {
		task = *s.task
	}
	goal := change.Goal
	if change.Kind == intent.Followup || change.Kind == intent.Cancelled {
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
	if s.task != nil && (change.Kind == intent.Redirect || change.Kind == intent.NewRequest) {
		state := "superseded"
		if s.intentRevision != nil && s.intentRevision.Kind == intent.Cancelled {
			state = "cancelled"
		}
		s.archiveRequirements(state)
	}
	same := s.task != nil && s.intentRevision != nil && s.task.ID == task.ID && s.intentRevision.Number == r.Number
	s.task, s.intentRevision, s.taskLink = &task, &r, &link
	s.alignmentGraph = nil
	s.linkedSummary = nil
	s.linkedPending = nil
	s.linkedGeneration++
	s.linkedAt = time.Time{}
	if s.linkedCancel != nil {
		s.linkedCancel()
	}
	if !same {
		s.loadFailedAttempts()
	}
	// Evidence is bound to an intent revision, including attributable followups.
	// Do not keep a green completion count from the previous revision.
	s.temporalStart = time.Now()
	if s.eng != nil {
		s.eng.ResetReliability()
	}
	s.checks = map[string]CheckResult{}
	s.parser.Session.TaskID, s.parser.Session.TaskRevision = task.ID, r.Number
	if change.Kind != intent.Followup && change.Kind != intent.Cancelled {
		s.firstPrompt = r.Goal
		if s.eng != nil {
			if change.Kind == intent.NewRequest || change.Kind == intent.Redirect {
				s.eng.VerificationStart = source.At
			}
			s.eng.Retarget(r.Goal)
		}
	} else if s.eng != nil {
		s.eng.AddRequest(change.Goal)
	}
	return nil
}

func (s *Session) recordIntentCommand(change intent.Change) error {
	ev := &event.Event{PromptOrigin: "user", Kind: event.KindPrompt, TS: time.Now(), Summary: "explicit intent command", Text: change.Goal, Basis: "live"}
	s.parser.AddEvent(ev)
	s.observe(ev)
	return s.recordIntent(change, intentSource(s, ev))
}

func (s *Session) goalCard(requested string) intent.Card {
	card := intent.GoalCard(s.c, s.acc, requested, nil)
	card.TemporalConflict = s.c != nil && s.c.TemporalConflict(s.Cfg.Detectors.S8.CeilingHours*3600)
	if r := s.intentRevision; r != nil {
		if card.Goal == "" {
			card.Goal = r.Goal
		}
		card.TaskID, card.Revision, card.Source = r.TaskID, r.Number, &r.Source
		card.LatestMessage = r.Message
	}
	if s.eng != nil {
		events := s.eng.St.Events
		if s.intentRevision != nil {
			for i, ev := range events {
				if !ev.TS.Before(s.intentRevision.Source.At) {
					events = events[i:]
					break
				}
				if i == len(events)-1 {
					events = nil
				}
			}
		}
		card.Work = intent.DescribeWork(s.eng.LatestRequest(), events)
		card.Output = intent.InferOutput(s.eng.RequestText())
	}
	return card
}

// Rebuild all still-active requests from their durable revisions. A redirect
// starts a new goal; ordinary followups never erase earlier requirements.
func (s *Session) restoreRequests() error {
	if s.db == nil || s.task == nil {
		return nil
	}
	revisions, err := s.db.TaskRevisions(s.task.ID)
	if err != nil {
		return err
	}
	s.eng.FirstPrompt = ""
	s.eng.Requests = nil
	s.archivedRequirements = nil
	previousCancelled := false
	for _, r := range revisions {
		if s.intentRevision != nil && r.Number > s.intentRevision.Number {
			break
		}
		if r.Kind == intent.Followup || r.Kind == intent.Cancelled {
			s.eng.AddRequest(r.Message)
		} else {
			if r.Kind == intent.NewRequest || r.Kind == intent.Redirect {
				s.eng.VerificationStart = r.Source.At
			}
			if s.eng.FirstPrompt != r.Goal {
				state := "superseded"
				if previousCancelled {
					state = "cancelled"
				}
				s.archiveRequirements(state)
			}
			s.eng.Retarget(r.Goal)
		}
		previousCancelled = r.Kind == intent.Cancelled
	}
	return nil
}
