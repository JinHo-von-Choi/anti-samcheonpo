// Package config holds the .samcheonpo.yml schema and defaults.
package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/JinHo-von-Choi/anti-samcheonpo/internal/sockpath"
)

// Config is the merged configuration (defaults < ~/.samcheonpo/config.yml < project .samcheonpo.yml).
type Config struct {
	Language                 string      `yaml:"language" json:"language"`
	Currency                 string      `yaml:"currency" json:"currency"`
	Levels                   Levels      `yaml:"levels" json:"levels"`
	Detectors                Detectors   `yaml:"detectors" json:"detectors"`
	VerifyCommands           []string    `yaml:"verify_commands" json:"verify_commands"`
	NondeterministicCommands []string    `yaml:"nondeterministic_commands" json:"nondeterministic_commands"`
	Contract                 ContractCfg `yaml:"contract" json:"contract"`
	Notify                   Notify      `yaml:"notify" json:"notify"`
	Privacy                  Privacy     `yaml:"privacy" json:"privacy"`
	Experiment               Experiment  `yaml:"experiment" json:"experiment"`
	Checkpoint               Checkpoint  `yaml:"checkpoint" json:"checkpoint"`
	Rollout                  Rollout     `yaml:"rollout" json:"rollout"`
	// IronLaws links the iron-laws checker (path empty: look it up on PATH).
	IronLaws struct {
		Enabled bool   `yaml:"enabled" json:"enabled"`
		Path    string `yaml:"path" json:"path"`
	} `yaml:"iron_laws" json:"iron_laws"`
}

// Levels configures the intervention ladder.
type Levels struct {
	NudgeCooldownEvents int      `yaml:"nudge_cooldown_events" json:"nudge_cooldown_events"`
	PauseOn             []string `yaml:"pause_on" json:"pause_on"`
	MaxStopBlocks       int      `yaml:"max_stop_blocks" json:"max_stop_blocks"`
}

// Rollout is a user-controlled launch stage. Evidence hashes are declared by
// an operator after reviewing evaluation reports; they are not certifications.
type Rollout struct {
	Mode           string            `yaml:"mode" json:"mode"` // shadow | recommend | validated
	ValidatedRules map[string]string `yaml:"validated_rules" json:"validated_rules"`
	// EscalateMaxBlocks caps per-session blocks of rules whose advice the
	// agent already received and repeated. 0 keeps every inferred rule advisory.
	EscalateMaxBlocks int `yaml:"escalate_max_blocks" json:"escalate_max_blocks"`
	// ShadowRules are recorded but never delivered or escalated, whatever the
	// mode: new rules start here until their false positive rate is measured.
	// A project file can add rules but not remove the user's.
	ShadowRules []string `yaml:"shadow_rules" json:"shadow_rules"`
}

// Detectors holds thresholds; all are initial values tuned on the calibration set.
type Detectors struct {
	S1 struct {
		RepeatNudgeAt int     `yaml:"repeat_nudge_at" json:"repeat_nudge_at"`
		RatioWindow   int     `yaml:"ratio_window" json:"ratio_window"`
		Ratio         float64 `yaml:"ratio" json:"ratio"`
		TestBloat     float64 `yaml:"test_bloat" json:"test_bloat"`
	} `yaml:"s1_verify_treadmill" json:"s1"`
	S2 struct {
		Nudge         int `yaml:"nudge" json:"nudge"`
		Notify        int `yaml:"notify" json:"notify"`
		Pause         int `yaml:"pause" json:"pause"`
		WhackAttempts int `yaml:"whack_attempts" json:"whack_attempts"`
	} `yaml:"s2_failure_loop" json:"s2"`
	S3 struct {
		NotifyFiles        int     `yaml:"notify_files" json:"notify_files"`
		JudgeEveryEvents   int     `yaml:"judge_every_events" json:"judge_every_events"`
		MaxWatchCostRatio  float64 `yaml:"max_watch_cost_ratio" json:"max_watch_cost_ratio"`
		MaxJudgeCalls      int     `yaml:"max_judge_calls" json:"max_judge_calls"`
		MaxWatchKRW        int64   `yaml:"max_watch_krw" json:"max_watch_krw"`
		JudgeTimeoutSec    int     `yaml:"judge_timeout_sec" json:"judge_timeout_sec"`
		MaxJudgeInputBytes int     `yaml:"max_judge_input_bytes" json:"max_judge_input_bytes"`
		// Judge enables the semantic drift judgement (off unless a provider is set).
		Judge struct {
			Provider  string `yaml:"provider" json:"provider"`
			Model     string `yaml:"model" json:"model"`
			BaseURL   string `yaml:"base_url" json:"base_url"`
			APIKeyEnv string `yaml:"api_key_env" json:"api_key_env"`
		} `yaml:"judge" json:"judge"`
	} `yaml:"s3_drift" json:"s3"`
	S4 struct {
		MaxReads    int `yaml:"max_reads" json:"max_reads"`
		MaxMinutes  int `yaml:"max_minutes" json:"max_minutes"`
		RereadCount int `yaml:"reread_count" json:"reread_count"`
	} `yaml:"s4_idle_explore" json:"s4"`
	S7 struct {
		Compactions int `yaml:"compactions" json:"compactions"`
	} `yaml:"s7_memory_rot" json:"s7"`
	S8 struct {
		VelocityMultiplier     float64 `yaml:"velocity_multiplier" json:"velocity_multiplier"`
		VelocityFloorKRWPerMin int64   `yaml:"velocity_floor_krw_per_min" json:"velocity_floor_krw_per_min"`
		IdleSpendFloorKRW      int64   `yaml:"idle_spend_floor_krw" json:"idle_spend_floor_krw"`
		IdleSpendBudgetRatio   float64 `yaml:"idle_spend_budget_ratio" json:"idle_spend_budget_ratio"`
		IdleMinToolEvents      int     `yaml:"idle_min_tool_events" json:"idle_min_tool_events"`
	} `yaml:"s8_cost" json:"s8"`
	// Overrides raises per-rule thresholds after confirmed false positives.
	Overrides map[string]int `yaml:"overrides" json:"overrides"`
}

// ContractCfg configures contract confirmation.
type ContractCfg struct {
	// Draft asks the agent for a contract draft on a new task: on | off.
	// Off (default) starts observation-only; a contract file the user keeps
	// still gets revision drafts and is enforced once accepted.
	Draft          string `yaml:"draft" json:"draft"`
	Confirm        string `yaml:"confirm" json:"confirm"` // always | budget_over | never
	ConfirmOverKRW int64  `yaml:"confirm_over_krw" json:"confirm_over_krw"`
}

// Notify configures user-facing channels.
type Notify struct {
	Desktop bool `yaml:"desktop" json:"desktop"`
	Push    struct {
		Provider string `yaml:"provider" json:"provider"` // ntfy | webhook
		Topic    string `yaml:"topic" json:"topic"`
		URL      string `yaml:"url" json:"url"`
	} `yaml:"push" json:"push"`
}

// Privacy configures data handling.
type Privacy struct {
	ExternalJudge bool `yaml:"external_judge" json:"external_judge"`
	StoreRawText  bool `yaml:"store_raw_text" json:"store_raw_text"`
}

// Experiment configures the intervention experiment.
type Experiment struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
}

// Checkpoint configures the checkpoint runner.
type Checkpoint struct {
	TimeoutSec int `yaml:"timeout_sec" json:"timeout_sec"`
}

// Default returns the initial configuration.
func Default() Config {
	c := Config{Language: "ko", Currency: "KRW"}
	c.Rollout.Mode = "recommend"
	c.Rollout.EscalateMaxBlocks = 3
	c.Rollout.ShadowRules = []string{"s1.verify_after_docs", "s1.review_repeat"}
	c.Levels = Levels{NudgeCooldownEvents: 5, PauseOn: []string{"same_error_8", "budget_100", "protect_path"}, MaxStopBlocks: 2}
	c.Detectors.S1.RepeatNudgeAt = 3
	c.Detectors.S1.RatioWindow = 20
	c.Detectors.S1.Ratio = 4
	c.Detectors.S1.TestBloat = 3
	c.Detectors.S2.Nudge, c.Detectors.S2.Notify, c.Detectors.S2.Pause = 3, 5, 8
	c.Detectors.S2.WhackAttempts = 5
	c.Detectors.S3.NotifyFiles = 3
	c.Detectors.S3.JudgeEveryEvents = 15
	c.Detectors.S3.MaxWatchCostRatio = 0.02
	c.Detectors.S3.MaxJudgeCalls = 8
	c.Detectors.S3.MaxWatchKRW = 1000
	c.Detectors.S3.JudgeTimeoutSec = 10
	c.Detectors.S3.MaxJudgeInputBytes = 8192
	c.Detectors.S4.MaxReads = 20
	c.Detectors.S4.MaxMinutes = 10
	c.Detectors.S4.RereadCount = 3
	c.Detectors.S7.Compactions = 2
	c.Detectors.S8.VelocityMultiplier = 3
	c.Detectors.S8.VelocityFloorKRWPerMin = 300
	c.Detectors.S8.IdleSpendFloorKRW = 5000
	c.Detectors.S8.IdleMinToolEvents = 30
	c.Detectors.S8.IdleSpendBudgetRatio = 0.15
	c.Contract = ContractCfg{Draft: "off", Confirm: "budget_over", ConfirmOverKRW: 3000}
	c.Notify.Desktop = true
	c.Privacy = Privacy{ExternalJudge: false, StoreRawText: false}
	c.Experiment.Enabled = false
	c.Checkpoint.TimeoutSec = 120
	c.IronLaws.Enabled = true
	return c
}

// Home returns the samcheonpo home directory (~/.samcheonpo or $SAMCHEONPO_HOME).
func Home() string { return sockpath.Home() }

// Load merges defaults, the user config and the project config.
func Load(projectDir string) (Config, string, error) {
	c := Default()
	for i, p := range []string{filepath.Join(Home(), "config.yml"), filepath.Join(projectDir, ".samcheonpo.yml")} {
		if projectDir == "" && p != filepath.Join(Home(), "config.yml") {
			continue
		}
		b, err := os.ReadFile(p)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return c, "", fmt.Errorf("설정 파일 읽기 실패: %w", err)
		}
		allowExternal := c.Privacy.ExternalJudge
		userIronLaws := c.IronLaws
		userPush := c.Notify.Push
		userJudge := c.Detectors.S3.Judge
		userExperiment := c.Experiment.Enabled
		userDetectors, userLevels := c.Detectors, c.Levels
		userLevels.PauseOn = append([]string(nil), c.Levels.PauseOn...)
		userMode := c.Rollout.Mode
		userEscalate := c.Rollout.EscalateMaxBlocks
		userShadow := append([]string(nil), c.Rollout.ShadowRules...)
		userRules := make(map[string]string, len(c.Rollout.ValidatedRules))
		for rule, digest := range c.Rollout.ValidatedRules {
			userRules[rule] = digest
		}
		if err := yaml.Unmarshal(b, &c); err != nil {
			return c, "", fmt.Errorf("%s: %w", p, err)
		}
		if i == 1 {
			// Repository settings can restrict, but cannot grant, external access.
			c.Privacy.ExternalJudge = allowExternal && c.Privacy.ExternalJudge
			// Programs to run and destinations to send to belong to the user.
			// A repository can switch them off but cannot add or redirect them.
			c.IronLaws.Enabled = userIronLaws.Enabled && c.IronLaws.Enabled
			c.IronLaws.Path = userIronLaws.Path
			if c.Notify.Push != userPush {
				if c.Notify.Push.Provider == "" {
					c.Notify.Push.Topic, c.Notify.Push.URL = "", ""
				} else {
					c.Notify.Push = userPush
				}
			}
			if c.Detectors.S3.Judge != userJudge {
				if c.Detectors.S3.Judge.Provider == "" {
					c.Detectors.S3.Judge = userJudge
					c.Detectors.S3.Judge.Provider = ""
				} else {
					c.Detectors.S3.Judge = userJudge
				}
			}
			c.Experiment.Enabled = userExperiment && c.Experiment.Enabled
			loosenOnly(&c.Detectors, &c.Levels, userDetectors, userLevels)
			rank := map[string]int{"shadow": 0, "recommend": 1, "validated": 2}
			if rank[c.Rollout.Mode] > rank[userMode] {
				c.Rollout.Mode = userMode
			}
			if c.Rollout.EscalateMaxBlocks > userEscalate {
				c.Rollout.EscalateMaxBlocks = userEscalate
			}
			for _, rule := range userShadow {
				if !slices.Contains(c.Rollout.ShadowRules, rule) {
					c.Rollout.ShadowRules = append(c.Rollout.ShadowRules, rule)
				}
			}
			for rule, digest := range c.Rollout.ValidatedRules {
				if userRules[rule] != digest {
					delete(c.Rollout.ValidatedRules, rule)
				}
			}
		}
	}
	if c.Rollout.Mode != "shadow" && c.Rollout.Mode != "recommend" && c.Rollout.Mode != "validated" {
		return c, "", fmt.Errorf("rollout.mode must be shadow, recommend, or validated")
	}
	if c.Contract.Draft != "on" && c.Contract.Draft != "off" {
		return c, "", fmt.Errorf("contract.draft must be on or off")
	}
	if c.Rollout.EscalateMaxBlocks < 0 {
		return c, "", fmt.Errorf("rollout.escalate_max_blocks must be 0 or greater")
	}
	if c.Detectors.Overrides == nil {
		c.Detectors.Overrides = map[string]int{}
	}
	return c, Hash(c), nil
}

// loosenOnly keeps project thresholds from being stricter than the user's:
// for each "smaller fires sooner" value the larger one wins, more stop blocks
// and new pause triggers are dropped. Detector overrides come from the
// user's own feedback and are never taken from a project file.
func loosenOnly(d *Detectors, l *Levels, ud Detectors, ul Levels) {
	atLeast := func(v *int, u int) {
		if *v < u {
			*v = u
		}
	}
	atLeastF := func(v *float64, u float64) {
		if *v < u {
			*v = u
		}
	}
	atLeast64 := func(v *int64, u int64) {
		if *v < u {
			*v = u
		}
	}
	atLeast(&d.S1.RepeatNudgeAt, ud.S1.RepeatNudgeAt)
	atLeastF(&d.S1.Ratio, ud.S1.Ratio)
	atLeastF(&d.S1.TestBloat, ud.S1.TestBloat)
	atLeast(&d.S2.Nudge, ud.S2.Nudge)
	atLeast(&d.S2.Notify, ud.S2.Notify)
	atLeast(&d.S2.Pause, ud.S2.Pause)
	atLeast(&d.S2.WhackAttempts, ud.S2.WhackAttempts)
	atLeast(&d.S3.NotifyFiles, ud.S3.NotifyFiles)
	atLeast(&d.S4.MaxReads, ud.S4.MaxReads)
	atLeast(&d.S4.MaxMinutes, ud.S4.MaxMinutes)
	atLeast(&d.S4.RereadCount, ud.S4.RereadCount)
	atLeast(&d.S7.Compactions, ud.S7.Compactions)
	atLeastF(&d.S8.VelocityMultiplier, ud.S8.VelocityMultiplier)
	atLeast64(&d.S8.VelocityFloorKRWPerMin, ud.S8.VelocityFloorKRWPerMin)
	atLeast64(&d.S8.IdleSpendFloorKRW, ud.S8.IdleSpendFloorKRW)
	atLeastF(&d.S8.IdleSpendBudgetRatio, ud.S8.IdleSpendBudgetRatio)
	atLeast(&d.S8.IdleMinToolEvents, ud.S8.IdleMinToolEvents)
	d.Overrides = ud.Overrides
	atLeast(&l.NudgeCooldownEvents, ul.NudgeCooldownEvents)
	if l.MaxStopBlocks > ul.MaxStopBlocks {
		l.MaxStopBlocks = ul.MaxStopBlocks
	}
	var pause []string
	for _, p := range l.PauseOn {
		if slices.Contains(ul.PauseOn, p) {
			pause = append(pause, p)
		}
	}
	l.PauseOn = pause
}

// Hash returns a stable hash of the effective configuration.
func Hash(c Config) string {
	b, _ := yaml.Marshal(c)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:16])
}

// Template is the file written by `samcheonpo init`.
const Template = `# samcheonpo project settings
language: ko
currency: KRW
# shadow: observe only; recommend: advice first (default)
# validated requires user-level rule -> reviewed evaluation SHA256 declarations
# escalate_max_blocks: a rule whose advice the agent already received and then
# repeated is blocked, at most this many times per session (0 = never block)
rollout:
  mode: recommend
  validated_rules: {}
  escalate_max_blocks: 3
  # rules recorded but not delivered until their false positives are measured
  shadow_rules: [s1.verify_after_docs, s1.review_repeat]
levels:
  nudge_cooldown_events: 5
  pause_on: [same_error_8, budget_100, protect_path]
detectors:
  s1_verify_treadmill: {repeat_nudge_at: 3, ratio_window: 20, ratio: 4}
  s2_failure_loop: {nudge: 3, notify: 5, pause: 8}
  s4_idle_explore: {max_reads: 20, max_minutes: 10}
  s8_cost: {velocity_multiplier: 3, idle_spend_floor_krw: 5000, idle_spend_budget_ratio: 0.15, idle_min_tool_events: 30}
# commands that are expected to give different results on rerun (flaky tests, polling)
nondeterministic_commands: []
# extra commands to treat as verification
verify_commands: []
contract:
  # on: ask the agent for a contract draft on each new task; off: observe only
  draft: off
  confirm: budget_over
  confirm_over_krw: 3000
notify:
  desktop: true
  push: {provider: "", topic: "", url: ""}
privacy:
  external_judge: false
  store_raw_text: false
# research only: randomly withholds some advice to measure its effect
experiment:
  enabled: false
`
