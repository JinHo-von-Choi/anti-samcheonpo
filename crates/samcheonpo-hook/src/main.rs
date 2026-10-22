//! The lightweight hook transport.
//!
//! One request, one socket, then exit. Every failure path prints nothing
//! and exits 0, which Claude Code treats as "proceed".
//!
//! The rules in this file mirror `internal/hookclient/hookclient.go`. An
//! agent that cannot read the output blocks the tool call, so the output
//! shape is part of the contract, not a convenience.

use std::io::{Read, Write};
use std::time::{Instant, SystemTime, UNIX_EPOCH};

use serde::Serialize;

use samcheonpo_hook::agent::{fail_closed_allow, output_is_readable, timeout_for};
use samcheonpo_hook::ipc::{UnixStream, COLD_START_INTERVAL, COLD_START_TIMEOUT, CONNECT_TIMEOUT};
use samcheonpo_hook::protocol::Response;
use samcheonpo_hook::spawn::{daemon_executable, no_spawn, spawn_daemon};
use samcheonpo_hook::wire;

/// The version string the Go CLI and this transport share.
const VERSION: &str = env!("SAMCHEONPO_HOOK_VERSION");

/// The largest hook payload the transport reads.
const MAX_PAYLOAD: usize = 32 << 20;

/// Reports the transport handling, not whether the agent read or obeyed
/// advice. No request text, tool arguments, or response content is
/// included.
#[derive(Serialize)]
struct Outcome {
    status: &'static str,
    timed_out: bool,
    response_received: bool,
    #[serde(skip_serializing_if = "is_zero")]
    handling_ns: i64,
}

fn is_zero(v: &i64) -> bool {
    *v == 0
}

fn main() {
    let diagnostics = std::env::var("SAMCHEONPO_HOOK_DIAGNOSTICS").unwrap_or_default() == "1";
    // A panic must not end the process with a non-zero exit code: Copilot
    // reads a failed preToolUse hook as a denial. The panic message goes to
    // stderr only when the diagnostics channel is free.
    std::panic::set_hook(Box::new(move |info| {
        if !diagnostics {
            eprintln!("samcheonpo-hook: {info}");
        }
    }));
    let outcome = std::panic::catch_unwind(run).unwrap_or(Outcome {
        status: "invalid_input",
        timed_out: false,
        response_received: false,
        handling_ns: 0,
    });
    if diagnostics {
        if let Ok(text) = serde_json::to_string(&outcome) {
            let mut err = std::io::stderr();
            let _ = err.write_all(text.as_bytes());
            let _ = err.write_all(b"\n");
            let _ = err.flush();
        }
    }
    std::process::exit(0);
}

/// The transport body. It always returns; the process exit code is 0.
fn run() -> Outcome {
    let started = Instant::now();
    let mut outcome = Outcome {
        status: "invalid_input",
        timed_out: false,
        response_received: false,
        handling_ns: 0,
    };
    let args: Vec<String> = std::env::args().collect();
    if args.len() == 2 && args[1] == "--version" {
        println!("{VERSION}");
        return outcome;
    }
    if (args.len() != 3 && args.len() != 4) || args[1] != "hook" {
        return outcome;
    }
    let event = args[2].clone();
    let agent = if args.len() == 4 {
        args[3].clone()
    } else {
        "claude".to_string()
    };

    let allow = fail_closed_allow(&agent, &event);
    let mut stdout = std::io::stdout();

    let mut payload_bytes = Vec::new();
    let _ = std::io::stdin()
        .take(MAX_PAYLOAD as u64)
        .read_to_end(&mut payload_bytes);
    // The Go transport checks the payload with `json.Valid` before it
    // connects, and an unreadable payload is a permission the agent reads as
    // a block. The compaction happens here so the frame carries the same
    // bytes as the Go frame.
    let payload = match wire::compact(&payload_bytes) {
        Some(p) => p,
        None => return pass(&mut outcome, started, allow, &mut stdout),
    };

    let sock = samcheonpo_hook::sockpath::path();
    let mut conn = match connect(&sock) {
        Ok(c) => c,
        Err(e) => {
            outcome.status = "connect_failed";
            outcome.timed_out = is_timeout(&e);
            return pass(&mut outcome, started, allow, &mut stdout);
        }
    };

    let timeout = timeout_for(&event, &agent);
    let deadline = Instant::now() + timeout;
    let deadline_unix_ms = unix_millis() + timeout.as_millis() as i64;
    let _ = conn.set_read_timeout(Some(timeout));
    let _ = conn.set_write_timeout(Some(timeout));
    let _ = &deadline;

    let frame = wire::request_frame(&agent, &event, deadline_unix_ms, &payload);
    if let Err(e) = samcheonpo_hook::ipc::write_frame(&mut conn, &frame) {
        outcome.status = "write_failed";
        outcome.timed_out = is_timeout(&e);
        return pass(&mut outcome, started, allow, &mut stdout);
    }

    let line = match samcheonpo_hook::ipc::read_frame(&mut conn, deadline) {
        Ok(l) => l,
        Err(e) => {
            outcome.status = "read_failed";
            outcome.timed_out = is_timeout(&e);
            return pass(&mut outcome, started, allow, &mut stdout);
        }
    };
    let response: Response = match serde_json::from_slice(&line) {
        Ok(r) => r,
        Err(_) => {
            outcome.status = "invalid_response";
            return pass(&mut outcome, started, allow, &mut stdout);
        }
    };
    outcome.response_received = true;
    if !response.error.is_empty() {
        outcome.status = "daemon_error";
        return pass(&mut outcome, started, allow, &mut stdout);
    }
    outcome.status = "ok";
    if !response.has_output() {
        return pass(&mut outcome, started, allow, &mut stdout);
    }
    let raw = response.output.as_ref().unwrap();
    let value: serde_json::Value = match serde_json::from_str(raw.get()) {
        Ok(v) => v,
        Err(_) => {
            outcome.status = "invalid_response";
            return pass(&mut outcome, started, allow, &mut stdout);
        }
    };
    if !output_is_readable(&agent, &event, &value) {
        outcome.status = "invalid_response";
        return pass(&mut outcome, started, allow, &mut stdout);
    }

    let mut out = raw.get().as_bytes().to_vec();
    out.push(b'\n');
    match stdout.write_all(&out) {
        Ok(()) => {
            let _ = stdout.flush();
            if !response.delivery_token.is_empty() {
                let mut ack_frame = wire::ack_frame(&response.delivery_token, true);
                ack_frame.push(b'\n');
                let _ = conn.write_all(&ack_frame);
                let _ = conn.flush();
            }
        }
        Err(_) => outcome.status = "output_failed",
    }
    record(&mut outcome, started)
}

/// The failure answer: the agent's "proceed" object, if it needs one.
fn pass(
    outcome: &mut Outcome,
    started: Instant,
    allow: &str,
    stdout: &mut std::io::Stdout,
) -> Outcome {
    if !allow.is_empty() {
        let _ = stdout.write_all(allow.as_bytes());
        let _ = stdout.write_all(b"\n");
        let _ = stdout.flush();
    }
    record(outcome, started)
}

/// The success answer: the output was printed, nothing more is added.
fn record(outcome: &mut Outcome, started: Instant) -> Outcome {
    outcome.handling_ns = started.elapsed().as_nanos() as i64;
    Outcome {
        status: outcome.status,
        timed_out: outcome.timed_out,
        response_received: outcome.response_received,
        handling_ns: outcome.handling_ns,
    }
}

/// Connects to the daemon, starting it when it is not up yet.
///
/// A request is written once. If the first write is answered, the answer is
/// used; if not, the hook gives up rather than sending the request again,
/// because a duplicate tool record or a stale intent is worse than a
/// missing event.
fn connect(sock: &str) -> std::io::Result<UnixStream> {
    if no_spawn() {
        return samcheonpo_hook::ipc::connect(sock, CONNECT_TIMEOUT);
    }
    if let Ok(c) = samcheonpo_hook::ipc::connect(sock, CONNECT_TIMEOUT) {
        return Ok(c);
    }
    spawn_daemon(&daemon_executable(&current_exe()));
    let deadline = Instant::now() + COLD_START_TIMEOUT;
    loop {
        std::thread::sleep(COLD_START_INTERVAL);
        let remaining = deadline.saturating_duration_since(Instant::now());
        if remaining.is_zero() {
            break;
        }
        if let Ok(c) = samcheonpo_hook::ipc::connect(sock, remaining.min(CONNECT_TIMEOUT)) {
            return Ok(c);
        }
    }
    samcheonpo_hook::ipc::connect(sock, CONNECT_TIMEOUT)
}

fn current_exe() -> std::path::PathBuf {
    std::env::current_exe().unwrap_or_else(|_| std::path::PathBuf::from("samcheonpo-hook"))
}

fn unix_millis() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_millis() as i64)
        .unwrap_or(0)
}

fn is_timeout(e: &std::io::Error) -> bool {
    matches!(
        e.kind(),
        std::io::ErrorKind::WouldBlock | std::io::ErrorKind::TimedOut
    )
}
