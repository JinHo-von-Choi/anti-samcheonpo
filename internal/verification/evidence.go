// Package verification decides when a successful check remains valid. It has
// no dependency on the daemon, CLI, database, or shell execution.
package verification

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

const Version = "verification-evidence/2"

// Key contains independently observed input and environment identities. Empty
// fields are unknown, never wildcards. A task revision invalidates all evidence.
type Key struct {
	TaskID          string `json:"task_id"`
	Revision        uint64 `json:"revision"`
	CheckID         string `json:"check_id"`
	CommandHash     string `json:"command_hash"`
	InputHash       string `json:"input_hash"`
	EnvironmentHash string `json:"environment_hash"`
	RunnerVersion   string `json:"runner_version"`
}

func (k Key) Valid() bool {
	return k.TaskID != "" && k.Revision > 0 && k.CheckID != "" && k.CommandHash != "" && k.InputHash != "" && k.EnvironmentHash != "" && k.RunnerVersion != ""
}

func (k Key) Digest() string {
	b, _ := json.Marshal(k)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

type Evidence struct {
	Version       string    `json:"version"`
	ID            string    `json:"id"`
	Key           Key       `json:"key"`
	SessionID     string    `json:"session_id"`
	SourceEventID string    `json:"source_event_id"`
	ObservedAt    time.Time `json:"observed_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	Pass          bool      `json:"pass"`
	Complete      bool      `json:"complete"`
	SideEffect    bool      `json:"side_effect"`
	Flaky         bool      `json:"flaky"`
	Manual        bool      `json:"manual"`
	ExitCode      int       `json:"exit_code"`
	TimedOut      bool      `json:"timed_out"`
	ResultHash    string    `json:"result_hash"`
}

func (e Evidence) Validate() error {
	if e.Version != Version {
		return errors.New("unsupported evidence version")
	}
	if e.ID == "" || !e.Key.Valid() || e.SessionID == "" || e.SourceEventID == "" || e.ObservedAt.IsZero() || e.ResultHash == "" {
		return errors.New("incomplete evidence identity or provenance")
	}
	if !e.ExpiresAt.After(e.ObservedAt) {
		return errors.New("invalid evidence validity window")
	}
	if e.Pass && (e.ExitCode != 0 || e.TimedOut || !e.Complete || e.SideEffect) {
		return errors.New("contradictory passing evidence")
	}
	return nil
}

func Reusable(e Evidence, key Key, now time.Time) (bool, string) {
	if err := e.Validate(); err != nil {
		return false, err.Error()
	}
	if e.Key != key || !key.Valid() {
		return false, "input, environment, command, or revision changed"
	}
	if now.Before(e.ObservedAt) || !now.Before(e.ExpiresAt) {
		return false, "evidence expired or clock moved backwards"
	}
	if e.Manual {
		return false, "manual confirmation is separate"
	}
	if e.Flaky {
		return false, "flaky check requires a new observation"
	}
	if !e.Pass || !e.Complete || e.SideEffect || e.TimedOut || e.ExitCode != 0 {
		return false, "no reusable pass"
	}
	return true, "same accepted check, inputs, and environment already passed"
}
