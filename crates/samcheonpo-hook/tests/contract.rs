//! The transport contract.
//!
//! The same spec file drives the Go transport. Both implementations must
//! satisfy it: the agent dialect decides what the transport prints, and a
//! printed shape the agent cannot read is worse than silence.

use std::io::{BufRead, BufReader, Write};
use std::os::unix::net::{UnixListener, UnixStream};
use std::process::{Command, Stdio};
use std::sync::mpsc;
use std::time::{Duration, Instant};

use samcheonpo_hook::sockpath;
use serde::Deserialize;

const SPEC: &str = concat!(
    env!("CARGO_MANIFEST_DIR"),
    "/../../internal/hookclient/contract_cases.json"
);
const BINARY: &str = env!("CARGO_BIN_EXE_samcheonpo-hook");

#[derive(Deserialize)]
struct Spec {
    #[allow(dead_code)]
    version: u32,
    cases: Vec<Case>,
}

#[derive(Deserialize)]
struct Case {
    name: String,
    agent: String,
    event: String,
    daemon: String,
    #[serde(default)]
    response: String,
    #[serde(default)]
    stdin: String,
    #[serde(default)]
    expect_ack: String,
    expect: Expect,
}

#[derive(Deserialize)]
struct Expect {
    exit_code: i32,
    stdout: String,
    status: String,
    response_received: bool,
}

#[derive(Deserialize)]
struct Outcome {
    status: String,
    response_received: bool,
}

/// A fake daemon that answers one request and records the delivery receipt.
fn start_fake_daemon(sock: &str, response: &str, want_ack: bool) -> mpsc::Receiver<String> {
    let listener = UnixListener::bind(sock).expect("가상 데몬 소켓 열기 실패");
    let (tx, rx) = mpsc::channel();
    let response = response.to_string();
    std::thread::spawn(move || {
        let (mut conn, _) = match listener.accept() {
            Ok(c) => c,
            Err(_) => return,
        };
        let mut reader = BufReader::new(conn.try_clone().expect("소켓 복사 실패"));
        let mut request = String::new();
        if reader.read_line(&mut request).is_err() {
            return;
        }
        if conn.write_all(response.as_bytes()).is_err() || conn.write_all(b"\n").is_err() {
            return;
        }
        if !want_ack {
            return;
        }
        let _ = conn.set_read_timeout(Some(Duration::from_millis(500)));
        let mut ack = String::new();
        if reader.read_line(&mut ack).is_ok() {
            let _ = tx.send(ack.trim().to_string());
        }
    });
    rx
}

fn temp_home(counter: usize) -> std::path::PathBuf {
    let home = std::env::temp_dir().join(format!(
        "samcheonpo-hook-contract-{}-{}",
        std::process::id(),
        counter
    ));
    let _ = std::fs::remove_dir_all(&home);
    std::fs::create_dir_all(home.join("run")).expect("런 폴더 만들기 실패");
    home
}

#[test]
fn the_transport_satisfies_the_contract() {
    let raw = std::fs::read_to_string(SPEC).expect("계약 명세 읽기 실패");
    let spec: Spec = serde_json::from_str(&raw).expect("계약 명세 해석 실패");
    assert!(!spec.cases.is_empty(), "계약 명세에 사례가 없다");

    for (index, case) in spec.cases.iter().enumerate() {
        let home = temp_home(index);
        let sock = sockpath::for_home(
            &home.to_string_lossy(),
            &std::env::temp_dir().to_string_lossy(),
            &sockpath::user_id(),
        );
        let mut daemon = None;
        if case.daemon == "up" {
            daemon = Some(start_fake_daemon(
                &sock,
                &case.response,
                !case.expect_ack.is_empty(),
            ));
        }

        let mut child = Command::new(BINARY);
        child
            .arg("hook")
            .arg(&case.event)
            .arg(&case.agent)
            .env("SAMCHEONPO_HOME", &home)
            .env("SAMCHEONPO_NO_SPAWN", "1")
            .env("SAMCHEONPO_HOOK_DIAGNOSTICS", "1")
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped());
        let mut child = child.spawn().expect("훅 실행 실패");
        child
            .stdin
            .as_mut()
            .expect("표준입력 없음")
            .write_all(case.stdin.as_bytes())
            .expect("입력 쓰기 실패");
        drop(child.stdin.take());
        let out = child.wait_with_output().expect("훅 대기 실패");

        assert_eq!(
            out.status.code().unwrap_or(-1),
            case.expect.exit_code,
            "{}: 종료 코드",
            case.name
        );
        assert_eq!(
            String::from_utf8_lossy(&out.stdout),
            case.expect.stdout,
            "{}: 표준출력",
            case.name
        );
        let outcome: Outcome = serde_json::from_str(String::from_utf8_lossy(&out.stderr).trim())
            .unwrap_or_else(|e| {
                panic!(
                    "{}: 진단 출력 해석 실패({:?}): {e}",
                    case.name,
                    String::from_utf8_lossy(&out.stderr)
                );
            });
        assert_eq!(outcome.status, case.expect.status, "{}: 상태", case.name);
        assert_eq!(
            outcome.response_received, case.expect.response_received,
            "{}: 응답 수신",
            case.name
        );

        if !case.expect_ack.is_empty() {
            let rx = daemon.as_ref().expect("데몬이 없어야 한다");
            let got = rx
                .recv_timeout(Duration::from_secs(2))
                .unwrap_or_else(|e| panic!("{}: 전달 확인 회신 없음: {e}", case.name));
            assert_eq!(got, case.expect_ack, "{}: 전달 확인", case.name);
        }
        drop(daemon);
        let _ = std::fs::remove_dir_all(&home);
    }
}

/// The transport must never end with a non-zero exit code: Copilot reads a
/// failed preToolUse hook as a denial.
#[test]
fn the_transport_always_exits_zero() {
    let home = temp_home(9999);
    let sock = sockpath::for_home(
        &home.to_string_lossy(),
        &std::env::temp_dir().to_string_lossy(),
        &sockpath::user_id(),
    );
    let _ = UnixStream::connect(&sock).ok();
    for args in [
        vec![],
        vec!["hook"],
        vec!["hook", "PreToolUse", "claude", "extra"],
        vec!["not-hook", "PreToolUse"],
        vec!["--version"],
    ] {
        let mut child = Command::new(BINARY);
        child
            .args(&args)
            .env("SAMCHEONPO_HOME", &home)
            .env("SAMCHEONPO_NO_SPAWN", "1")
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped());
        let mut child = child.spawn().expect("훅 실행 실패");
        child
            .stdin
            .as_mut()
            .expect("표준입력 없음")
            .write_all(b"{\"broken\":")
            .expect("입력 쓰기 실패");
        drop(child.stdin.take());
        let started = Instant::now();
        let status = child.wait().expect("훅 대기 실패");
        assert_eq!(status.code().unwrap_or(-1), 0, "인자 {args:?}: 종료 코드");
        assert!(
            started.elapsed() < Duration::from_secs(5),
            "인자 {args:?}: 종료 지연"
        );
    }
}

/// The hook and the library must agree on the socket path, and both must
/// agree with the Go implementation.
#[test]
fn the_socket_path_follows_the_shared_rules() {
    let home = temp_home(9998);
    let tmp = std::env::temp_dir().to_string_lossy().to_string();
    let user = sockpath::user_id();
    let expected = sockpath::for_home(&home.to_string_lossy(), &tmp, &user);
    assert!(expected.ends_with("samcheonpo.sock") || expected.ends_with(".sock"));
    assert!(
        expected.len() <= 100,
        "소켓 경로가 제한을 넘었다: {}",
        expected.len()
    );
}
