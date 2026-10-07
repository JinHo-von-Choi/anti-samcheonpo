// Package event defines the agent-neutral event model that every adapter
// produces and every detector consumes.
package event

import "time"

// Kind is the coarse type of an event.
type Kind string

const (
	KindTool         Kind = "tool"
	KindMessage      Kind = "message"
	KindCompact      Kind = "compact"
	KindPrompt       Kind = "prompt"
	KindStop         Kind = "stop"
	KindSessionStart Kind = "session_start"
	KindSessionEnd   Kind = "session_end"
)

// Normalized tool names.
const (
	ToolShell  = "shell"
	ToolRead   = "read"
	ToolWrite  = "write"
	ToolEdit   = "edit"
	ToolSearch = "search"
	ToolWeb    = "web"
	ToolTodo   = "todo"
	ToolTask   = "task"
	ToolOther  = "other"
)

// Category is the ledger classification of an event's cost.
type Category string

const (
	CatProduce Category = "produce" // 생산
	CatVerify  Category = "verify"  // 검증
	CatExplore Category = "explore" // 탐색
	CatRepair  Category = "repair"  // 수리
	CatRevert  Category = "revert"  // 되돌림
	CatPlan    Category = "plan"    // 계획
	CatWatch   Category = "watch"   // 감시
)

// Usage is the token usage attributed to one event.
type Usage struct {
	In         int64 `json:"in"`
	Out        int64 `json:"out"`
	CacheRead  int64 `json:"cache_read"`
	CacheWrite int64 `json:"cache_write"`
	// CacheWrite1h is the part of CacheWrite written with the 1-hour TTL.
	CacheWrite1h int64  `json:"cache_write_1h,omitempty"`
	Model        string `json:"model,omitempty"`
}

// Total returns all tokens.
func (u Usage) Total() int64 { return u.In + u.Out + u.CacheRead + u.CacheWrite }

// Add accumulates another usage into u (model is kept if already set).
func (u *Usage) Add(o Usage) {
	u.In += o.In
	u.Out += o.Out
	u.CacheRead += o.CacheRead
	u.CacheWrite += o.CacheWrite
	u.CacheWrite1h += o.CacheWrite1h
	if u.Model == "" {
		u.Model = o.Model
	}
}

// Event is one normalized occurrence in an agent session.
type Event struct {
	SessionID   string            `json:"session_id"`
	Seq         int64             `json:"seq"`
	TS          time.Time         `json:"ts"`
	Kind        Kind              `json:"kind"`
	Tool        string            `json:"tool,omitempty"`
	RawTool     string            `json:"raw_tool,omitempty"`
	CallID      string            `json:"call_id,omitempty"`
	Cmd         string            `json:"-"` // raw command, never stored
	CmdNorm     string            `json:"cmd_norm,omitempty"`
	CmdFP       string            `json:"cmd_fp,omitempty"`
	Dir         string            `json:"dir,omitempty"`
	Paths       []string          `json:"paths,omitempty"`
	WriteHashes map[string]string `json:"write_hashes,omitempty"`
	// Edits carry old/new string hashes for edit events whose prior state is unknown.
	Edits        []EditRef      `json:"edits,omitempty"`
	AddedLines   map[string]int `json:"added_lines,omitempty"`
	RemovedLines map[string]int `json:"removed_lines,omitempty"`
	Deleted      []string       `json:"deleted,omitempty"`
	Created      []string       `json:"created,omitempty"` // files that did not exist before this write
	// Bucket is the receipt bucket after hindsight reclassification:
	// progress | explore | waste | other. Symptom names the waste detector.
	Bucket       string   `json:"bucket,omitempty"`
	Symptom      string   `json:"symptom,omitempty"`
	Estimated    bool     `json:"estimated,omitempty"` // cost flagged by an estimate-only verdict (확인 필요)
	WSBefore     string   `json:"ws_before,omitempty"`
	WSAfter      string   `json:"ws_after,omitempty"`
	ExitCode     *int     `json:"exit_code,omitempty"`
	IsError      bool     `json:"is_error,omitempty"`
	ErrFPs       []string `json:"err_fps,omitempty"`
	FailedTests  []string `json:"failed_tests,omitempty"`
	ResultFP     string   `json:"result_fp,omitempty"`
	Usage        Usage    `json:"usage"`
	CostMicroKRW int64    `json:"cost_micro_krw"`
	Priced       bool     `json:"priced"`
	Category     Category `json:"category,omitempty"`
	ShellClass   string   `json:"shell_class,omitempty"`
	Mutating     bool     `json:"mutating,omitempty"`
	Unknown      bool     `json:"unknown,omitempty"`
	Basis        string   `json:"basis,omitempty"` // live | hindsight
	Forced       bool     `json:"forced,omitempty"`
	Parent       bool     `json:"parent,omitempty"` // event came from a subagent transcript
	Summary      string   `json:"summary,omitempty"`
	// Purpose is a subagent call's kind and task description, used to tell
	// a repeated review from a different one.
	Purpose   string `json:"purpose,omitempty"`
	SourceRef string `json:"source_ref,omitempty"`
	// Text is transient analysis input (assistant message text, prompt text,
	// tool output excerpt). It is never written to the ledger.
	Text string `json:"-"`
	// Patch is transient (added lines of a write/edit), used by S5 rules.
	Patch []PatchFile `json:"-"`
}

// EditRef records an edit when the prior file content is unknown.
type EditRef struct {
	Path    string `json:"path"`
	OldHash string `json:"old_hash"`
	NewHash string `json:"new_hash"`
}

// PatchFile is a transient view of what a write changed in one file.
type PatchFile struct {
	Path    string
	Added   []string
	Removed []string
	Deleted bool
}

// Session is a parsed agent session.
type Session struct {
	// Optional v2 task association; omitted from legacy v1 canonical rows.
	TaskID       string    `json:"task_id,omitempty"`
	TaskRevision uint64    `json:"task_revision,omitempty"`
	ID           string    `json:"id"`
	Agent        string    `json:"agent"`
	Model        string    `json:"model"`
	ProjectPath  string    `json:"project_path"`
	StartedAt    time.Time `json:"started_at"`
	EndedAt      time.Time `json:"ended_at"`
	Mode         string    `json:"mode"` // live | audit
	SourcePath   string    `json:"source_path"`
	SourceHash   string    `json:"source_hash"`
	FirstPrompt  string    `json:"-"`
	Events       []*Event  `json:"-"`
	// Parse quality, used for format-change detection.
	ToolUses       int  `json:"tool_uses"`
	ToolUsesPaired int  `json:"tool_uses_paired"`
	UsageLines     int  `json:"usage_lines"`
	UsageParsed    int  `json:"usage_parsed"`
	FormatFailed   bool `json:"format_failed"`
	// Subscription quota information when the record carries it.
	QuotaUsedPct    float64 `json:"quota_used_pct,omitempty"`
	QuotaKnown      bool    `json:"quota_known,omitempty"`
	QuotaWindowMin  int     `json:"quota_window_min,omitempty"`
	ReportedCostUSD float64 `json:"reported_cost_usd,omitempty"`
}
