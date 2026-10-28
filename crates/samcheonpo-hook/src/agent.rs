//! Per-agent deadlines and output rules.
//!
//! The values mirror `internal/hookclient/hookclient.go` exactly. An agent
//! that cannot read the output blocks the tool call, so the "say nothing"
//! rule is part of the contract, not a preference.

use std::time::Duration;

use serde_json::Value;

/// Timeout per hook event.
///
/// Stop runs the contract checkpoint and SessionEnd writes the receipt, so
/// they wait longer than per-tool hooks. Claude Code gives SessionEnd hooks
/// 35 seconds (plugin setting) and the daemon budgets 30; Codex and the
/// opencode forwarder stop a hook after about 3 seconds.
pub fn timeout_for(event: &str, agent: &str) -> Duration {
    if agent == "copilot" {
        // Copilot names its events in camel case. A pre tool hook answers in
        // milliseconds and a stop hook runs the turn end work, so it waits a
        // little longer than a per tool hook.
        match event {
            "preToolUse" | "PreToolUse" => return Duration::from_millis(15),
            "agentStop" | "Stop" => return Duration::from_millis(2500),
            "sessionEnd" | "SessionEnd" => return Duration::from_millis(2500),
            _ => {}
        }
    }
    if agent == "mcode" {
        // MiniMax Code runs each hook as a process and waits for it up to the
        // timeoutSeconds the hooks file states, so a pre tool answer can take
        // the time a first verdict needs; a post tool answer carries advice
        // that a 50ms limit dropped in a real session (60ms measured).
        match event {
            "PreToolUse" => return Duration::from_millis(250),
            "PostToolUse" => return Duration::from_millis(500),
            _ => {}
        }
    }
    if agent == "cursor" {
        match event {
            "preToolUse" | "beforeShellExecution" | "beforeReadFile" | "beforeMCPExecution" => {
                return Duration::from_millis(15)
            }
            "sessionEnd" => return Duration::from_millis(2500),
            "stop" => return Duration::from_millis(2500),
            _ => {}
        }
    }
    match event {
        "PreToolUse" => Duration::from_millis(15),
        "Stop" => Duration::from_secs(150),
        "SessionEnd" => {
            if agent == "claude" {
                Duration::from_secs(30)
            } else {
                Duration::from_millis(2500)
            }
        }
        _ => Duration::from_millis(50),
    }
}

/// The explicit "proceed" for agents that block when a hook prints nothing.
///
/// Cursor permission hooks need a permission object. `agy` proceeds through
/// its own permission flow on empty output; an explicit "allow" there would
/// skip it, so agy stays silent.
pub fn fail_closed_allow(agent: &str, event: &str) -> &'static str {
    if agent != "cursor" {
        return "";
    }
    if event == "preToolUse"
        || event == "subagentStart"
        || (event.starts_with("before") && event != "beforeSubmitPrompt")
    {
        return r#"{"permission":"allow"}"#;
    }
    if event == "beforeSubmitPrompt" {
        return r#"{"continue":true}"#;
    }
    ""
}

/// True when the daemon's output can be given to this agent as is.
///
/// A shape the agent cannot read is dropped rather than printed: a wrong
/// shape is worse than no answer, because an unreadable permission object
/// blocks the action instead of allowing it.
pub fn output_is_readable(agent: &str, event: &str, output: &Value) -> bool {
    match agent {
        "cursor" if !fail_closed_allow(agent, event).is_empty() => {
            let Some(map) = output.as_object() else {
                return false;
            };
            if event == "beforeSubmitPrompt" {
                return matches!(map.get("continue"), Some(Value::Bool(_)));
            }
            let Some(permission) = map.get("permission").and_then(Value::as_str) else {
                return false;
            };
            match permission {
                "allow" | "deny" => true,
                "ask" => event != "preToolUse" && event != "subagentStart",
                _ => false,
            }
        }
        "copilot" => {
            let Some(map) = output.as_object() else {
                return false;
            };
            match event {
                "preToolUse" | "PreToolUse" => matches!(
                    map.get("permissionDecision").and_then(Value::as_str),
                    Some("allow") | Some("deny")
                ),
                "agentStop" | "Stop" => {
                    matches!(
                        map.get("decision").and_then(Value::as_str),
                        Some("block") | Some("allow")
                    )
                }
                _ => true,
            }
        }
        _ => true,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn pre_tool_use_budget_is_fifteen_milliseconds() {
        assert_eq!(
            timeout_for("PreToolUse", "claude"),
            Duration::from_millis(15)
        );
        assert_eq!(
            timeout_for("preToolUse", "copilot"),
            Duration::from_millis(15)
        );
        assert_eq!(
            timeout_for("beforeShellExecution", "cursor"),
            Duration::from_millis(15)
        );
    }

    #[test]
    fn mcode_waits_for_its_tool_answers() {
        assert_eq!(
            timeout_for("PreToolUse", "mcode"),
            Duration::from_millis(250)
        );
        assert_eq!(
            timeout_for("PostToolUse", "mcode"),
            Duration::from_millis(500)
        );
        assert_eq!(timeout_for("Stop", "mcode"), Duration::from_secs(150));
        assert_eq!(
            timeout_for("SessionEnd", "mcode"),
            Duration::from_millis(2500)
        );
    }

    #[test]
    fn session_end_budget_follows_the_agent() {
        assert_eq!(timeout_for("SessionEnd", "claude"), Duration::from_secs(30));
        assert_eq!(
            timeout_for("SessionEnd", "codex"),
            Duration::from_millis(2500)
        );
        assert_eq!(
            timeout_for("sessionEnd", "copilot"),
            Duration::from_millis(2500)
        );
    }

    #[test]
    fn only_cursor_permission_events_get_an_allow() {
        assert_eq!(
            fail_closed_allow("cursor", "beforeShellExecution"),
            r#"{"permission":"allow"}"#
        );
        assert_eq!(
            fail_closed_allow("cursor", "beforeSubmitPrompt"),
            r#"{"continue":true}"#
        );
        assert_eq!(fail_closed_allow("agy", "preToolUse"), "");
        assert_eq!(fail_closed_allow("claude", "PreToolUse"), "");
    }

    #[test]
    fn cursor_rejects_a_shape_it_cannot_read() {
        let ok = serde_json::json!({"permission": "deny"});
        assert!(output_is_readable("cursor", "beforeShellExecution", &ok));
        let wrong = serde_json::json!({"action": "allow"});
        assert!(!output_is_readable(
            "cursor",
            "beforeShellExecution",
            &wrong
        ));
    }

    #[test]
    fn copilot_accepts_only_allow_or_deny() {
        assert!(output_is_readable(
            "copilot",
            "preToolUse",
            &serde_json::json!({"permissionDecision": "deny"})
        ));
        assert!(!output_is_readable(
            "copilot",
            "preToolUse",
            &serde_json::json!({"permissionDecision": "ask"})
        ));
        assert!(!output_is_readable(
            "copilot",
            "preToolUse",
            &serde_json::json!({"decision": "block"})
        ));
    }
}
