// Package intent models task identity and user-originated intent revisions.
// It does not execute commands or infer permission from text.
package intent

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/fp"
	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/pathnorm"
	"path/filepath"
	"strings"
	"time"
)

const Version = "task-intent/2"

type Origin string

const (
	User     Origin = "user"
	Tool     Origin = "tool"
	Imported Origin = "imported"
)

// Source is assigned by the ingress adapter, not parsed from user/tool prose.
type Source struct {
	Origin    Origin    `json:"origin"`
	SessionID string    `json:"session_id"`
	EventID   string    `json:"event_id"`
	At        time.Time `json:"at"`
}

type Task struct {
	Version   string    `json:"version"`
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	CreatedAt time.Time `json:"created_at"`
}

// Revision records intent, never shell execution authority. Acceptance remains
// a separate operation on the exact contract file and its checks hash.
type Revision struct {
	Kind         ChangeKind `json:"kind,omitempty"`
	Message      string     `json:"message,omitempty"`
	Version      string     `json:"version"`
	TaskID       string     `json:"task_id"`
	Number       uint64     `json:"number"`
	Parent       uint64     `json:"parent"`
	Goal         string     `json:"goal"`
	ContractHash string     `json:"contract_hash"`
	Source       Source     `json:"source"`
	Draft        bool       `json:"draft"`
}

type SessionLink struct {
	TaskID          string `json:"task_id"`
	Agent           string `json:"agent"`
	SessionID       string `json:"session_id"`
	ParentAgent     string `json:"parent_agent,omitempty"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	// IncludesChildren prevents double-counting a merged parent transcript.
	IncludesChildren bool `json:"includes_children"`
}

func NewTask(projectID string, now time.Time) (Task, error) {
	if strings.TrimSpace(projectID) == "" || now.IsZero() {
		return Task{}, errors.New("project identity and creation time required")
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return Task{}, err
	}
	return Task{Version: Version, ID: hex.EncodeToString(id[:]), ProjectID: projectID, CreatedAt: now.UTC()}, nil
}

func (r Revision) Validate() error {
	switch r.Kind {
	case "", NewRequest, Followup, Redirect, Confirmed:
	default:
		return errors.New("unsupported intent change kind")
	}
	if r.Version != Version {
		return fmt.Errorf("unsupported intent version: %s", r.Version)
	}
	if r.TaskID == "" || r.Number == 0 || r.Parent != r.Number-1 || strings.TrimSpace(r.Goal) == "" {
		return errors.New("invalid intent revision")
	}
	if r.Source.Origin != User || r.Source.SessionID == "" || r.Source.EventID == "" || r.Source.At.IsZero() {
		return errors.New("intent requires attributable user input")
	}
	return nil
}

func Revise(taskID string, previous *Revision, goal string, source Source) (Revision, error) {
	r := Revision{Version: Version, TaskID: taskID, Number: 1, Goal: goal, Source: source, Draft: true}
	if previous != nil {
		if err := previous.Validate(); err != nil {
			return Revision{}, err
		}
		if previous.TaskID != taskID {
			return Revision{}, errors.New("revision belongs to another task")
		}
		r.Parent = previous.Number
		r.Number = previous.Number + 1
	}
	return r, r.Validate()
}

// ProjectID is the identity of a project directory. It is computed from the
// canonical path, so every spelling of the directory gives the same value.
func ProjectID(root string) string {
	abs, err := pathnorm.Absolute(root)
	if err != nil {
		abs = filepath.Clean(root)
	}
	return fp.Hash("project", filepath.Clean(abs))
}

// MatchesProject reports whether id names root, including ids recorded from
// the path as it was typed before identities were canonical.
func MatchesProject(id, root string) bool {
	spellings, err := pathnorm.Spellings(root)
	if err != nil {
		spellings = []string{filepath.Clean(root)}
	}
	for _, spelling := range spellings {
		if id == fp.Hash("project", filepath.Clean(spelling)) {
			return true
		}
	}
	return false
}
