//! Socket path resolution.
//!
//! This must produce the same byte sequence as the Go `internal/sockpath`
//! package, because the daemon listens on the path this module computes.
//! The two implementations are held together by `internal/sockpath`
//! regression vectors; see `docs/plans/2026-10-22-hybrid-rust-hook-and-governor-enhancement-plan.md`
//! work item 3.

use sha2::{Digest, Sha256};

/// Keeps the path under the unix socket limit (108 bytes on Linux, 104 on
/// macOS) including the terminator.
const MAX_LEN: usize = 100;

/// The samcheonpo home directory.
pub fn home() -> String {
    if let Ok(h) = std::env::var("SAMCHEONPO_HOME") {
        if !h.is_empty() {
            return h;
        }
    }
    let base = user_home_dir().unwrap_or_default();
    join(&[&base, ".samcheonpo"])
}

/// The daemon socket path.
///
/// `$XDG_RUNTIME_DIR/samcheonpo.sock` for the default home, else
/// `<home>/run/samcheonpo.sock`, so an explicit `SAMCHEONPO_HOME` never
/// shares the default daemon. A path too long for a unix socket is replaced
/// by a short per-user path derived from it.
pub fn path() -> String {
    let mut p = join(&[&home(), "run", "samcheonpo.sock"]);
    let xdg = std::env::var("XDG_RUNTIME_DIR").unwrap_or_default();
    if !xdg.is_empty()
        && std::env::var("SAMCHEONPO_HOME")
            .unwrap_or_default()
            .is_empty()
        && is_dir(&xdg)
    {
        p = join(&[&xdg, "samcheonpo.sock"]);
    }
    short_path(&p, &temp_dir(), &user_id())
}

/// The socket path for an explicit home, without touching the environment.
///
/// This mirrors the Go `sockpath.ForHome`, so a test or a caller can name
/// the same socket the hook will open.
pub fn for_home(home: &str, tmp: &str, user: &str) -> String {
    short_path(&join(&[home, "run", "samcheonpo.sock"]), tmp, user)
}

/// The numeric user id used in the short fallback path.
pub fn user_id() -> String {
    #[cfg(unix)]
    {
        let uid = unsafe { libc_getuid() };
        if uid >= 0 {
            return uid.to_string();
        }
    }
    #[cfg(not(unix))]
    {
        let uid = unsafe { libc_getuid() };
        if uid >= 0 {
            return uid.to_string();
        }
    }
    std::env::var("USERNAME").unwrap_or_default()
}

#[cfg(unix)]
unsafe fn libc_getuid() -> i32 {
    // getuid() is always available on unix targets; the crate avoids a libc
    // dependency by declaring the symbol directly.
    extern "C" {
        fn getuid() -> u32;
    }
    getuid() as i32
}

#[cfg(not(unix))]
unsafe fn libc_getuid() -> i32 {
    -1
}

/// Shortens a socket path that exceeds the platform limit.
pub fn short_path(p: &str, tmp: &str, user: &str) -> String {
    if p.len() <= MAX_LEN {
        return p.to_string();
    }
    let h = digest(p);
    let fallback = join(&[tmp, &format!("samcheonpo-{user}-{h}.sock")]);
    if fallback.len() <= MAX_LEN {
        return fallback;
    }
    // a long temp directory or user name: keep only what makes the name unique
    join(&[tmp, &format!("sc-{h}.sock")])
}

/// The first six bytes of the SHA-256 of `p`, in lowercase hex.
fn digest(p: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(p.as_bytes());
    let sum = hasher.finalize();
    let mut out = String::with_capacity(12);
    for byte in sum.iter().take(6) {
        out.push_str(&format!("{byte:02x}"));
    }
    out
}

fn is_dir(path: &str) -> bool {
    std::fs::metadata(path).map(|m| m.is_dir()).unwrap_or(false)
}

fn user_home_dir() -> Option<String> {
    #[cfg(windows)]
    {
        std::env::var("USERPROFILE").ok().filter(|v| !v.is_empty())
    }
    #[cfg(not(windows))]
    {
        std::env::var("HOME").ok().filter(|v| !v.is_empty())
    }
}

fn temp_dir() -> String {
    #[cfg(windows)]
    {
        for key in ["TMP", "TEMP", "USERPROFILE"] {
            if let Ok(v) = std::env::var(key) {
                if !v.is_empty() {
                    return v;
                }
            }
        }
        "C:\\Windows\\Temp".to_string()
    }
    #[cfg(not(windows))]
    {
        std::env::var("TMPDIR")
            .ok()
            .filter(|v| !v.is_empty())
            .unwrap_or_else(|| "/tmp".to_string())
    }
}

/// Joins path elements the way Go's `filepath.Join` does: elements are
/// separated by the platform separator and the result is cleaned.
pub fn join(elements: &[&str]) -> String {
    let sep = separator();
    let mut parts: Vec<&str> = Vec::with_capacity(elements.len());
    for e in elements {
        if !e.is_empty() {
            parts.push(e);
        }
    }
    if parts.is_empty() {
        return String::new();
    }
    let joined = parts.join(&sep.to_string());
    clean(&joined)
}

fn separator() -> char {
    if cfg!(windows) {
        '\\'
    } else {
        '/'
    }
}

/// Cleans a path the way Go's `filepath.Clean` does for slash separated
/// paths: it removes `.` elements, resolves `..` elements and collapses
/// repeated separators.
pub fn clean(p: &str) -> String {
    let sep = separator();
    if p.is_empty() {
        return ".".to_string();
    }
    let rooted = p.starts_with(sep);
    let mut out: Vec<&str> = Vec::new();
    for seg in p.split(sep) {
        match seg {
            "" | "." => {}
            ".." => match out.last() {
                Some(last) if *last != ".." => {
                    out.pop();
                }
                _ => {
                    if !rooted {
                        out.push("..");
                    }
                }
            },
            s => out.push(s),
        }
    }
    let mut joined = out.join(&sep.to_string());
    if rooted {
        joined.insert(0, sep);
    }
    if joined.is_empty() {
        return ".".to_string();
    }
    joined
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn short_path_keeps_short_paths() {
        assert_eq!(short_path("/tmp/a.sock", "/tmp", "1000"), "/tmp/a.sock");
    }

    #[test]
    fn short_path_replaces_long_paths() {
        let long = format!("/home/{}/{}", "u".repeat(90), "samcheonpo.sock");
        assert!(long.len() > MAX_LEN);
        let got = short_path(&long, "/tmp", "1000");
        assert!(got.starts_with("/tmp/samcheonpo-1000-"));
        assert!(got.ends_with(".sock"));
        assert!(got.len() <= MAX_LEN);
    }

    #[test]
    fn short_path_keeps_a_path_at_the_limit() {
        let exact = format!("/tmp/{}", "x".repeat(MAX_LEN - "/tmp/".len()));
        assert_eq!(exact.len(), MAX_LEN);
        assert_eq!(short_path(&exact, "/tmp", "1000"), exact);
    }

    #[test]
    fn clean_removes_dot_and_dotdot() {
        assert_eq!(clean("/a//b/./c"), "/a/b/c");
        assert_eq!(clean("/a/b/../c"), "/a/c");
        assert_eq!(clean("/.."), "/");
        assert_eq!(clean("a/../b"), "b");
    }

    #[test]
    fn join_matches_go_semantics() {
        assert_eq!(join(&["/home/u", ".samcheonpo"]), "/home/u/.samcheonpo");
        assert_eq!(
            join(&["/home/u/", "/run", "samcheonpo.sock"]),
            "/home/u/run/samcheonpo.sock"
        );
        assert_eq!(join(&["", "run"]), "run");
    }
}
