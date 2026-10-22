//! Daemon start-up and process helpers.

use std::path::{Path, PathBuf};

/// The environment variable that suppresses the cold start.
pub const NO_SPAWN_ENV: &str = "SAMCHEONPO_NO_SPAWN";

/// True when the operator asked the hook not to start a daemon.
pub fn no_spawn() -> bool {
    std::env::var(NO_SPAWN_ENV).unwrap_or_default() == "1"
}

/// The daemon executable that belongs to this hook.
///
/// A hook binary named `samcheonpo-hook` starts the sibling `samcheonpo`.
/// Any other name starts itself, which keeps single binary installs working.
pub fn daemon_executable(hook_exe: &Path) -> PathBuf {
    let name = hook_exe
        .file_name()
        .map(|n| n.to_string_lossy().to_lowercase())
        .unwrap_or_default();
    let name = name.strip_suffix(".exe").unwrap_or(&name);
    if name == "samcheonpo-hook" {
        let mut sibling = hook_exe.to_path_buf();
        sibling.set_file_name(exe_suffix("samcheonpo"));
        return sibling;
    }
    hook_exe.to_path_buf()
}

fn exe_suffix(base: &str) -> String {
    if cfg!(windows) {
        format!("{base}.exe")
    } else {
        base.to_string()
    }
}

/// Starts the daemon detached from this process.
///
/// The child must not keep the hook's stdin, stdout or stderr open, and it
/// must survive the hook exiting. Standard output and standard error go to
/// the null device, and the child gets its own session on unix.
pub fn spawn_daemon(exe: &Path) {
    if no_spawn() {
        return;
    }
    let mut command = std::process::Command::new(exe);
    command.arg("daemon");
    command.stdin(std::process::Stdio::null());
    command.stdout(std::process::Stdio::null());
    command.stderr(std::process::Stdio::null());
    detach(&mut command);
    // The daemon keeps running after this process exits, so the handle is
    // dropped without waiting: waiting here would hold the hook until the
    // daemon's idle timeout.
    let _child = command.spawn();
}

#[cfg(unix)]
fn detach(command: &mut std::process::Command) {
    use std::os::unix::process::CommandExt;
    unsafe {
        command.pre_exec(|| {
            // setsid() puts the daemon in its own session so it is not tied
            // to the hook's terminal or process group.
            extern "C" {
                fn setsid() -> i32;
            }
            setsid();
            Ok(())
        });
    }
}

#[cfg(windows)]
fn detach(command: &mut std::process::Command) {
    use std::os::windows::process::CommandExt;
    const DETACHED_PROCESS: u32 = 0x0000_0008;
    const CREATE_NEW_PROCESS_GROUP: u32 = 0x0000_0200;
    command.creation_flags(DETACHED_PROCESS | CREATE_NEW_PROCESS_GROUP);
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn a_hook_binary_starts_its_sibling() {
        let got = daemon_executable(Path::new("/opt/bin/samcheonpo-hook"));
        assert_eq!(got, PathBuf::from("/opt/bin/samcheonpo"));
    }

    #[test]
    fn another_binary_starts_itself() {
        let got = daemon_executable(Path::new("/opt/bin/other"));
        assert_eq!(got, PathBuf::from("/opt/bin/other"));
    }
}
