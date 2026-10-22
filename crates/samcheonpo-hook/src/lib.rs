//! The hook transport library.
//!
//! The transport uses a unix domain socket. On Windows the standard library
//! still keeps `std::os::windows::net::UnixStream` behind the unstable
//! `windows_unix_domain_sockets` feature (Rust 1.97.1), so the Rust
//! transport is built for unix targets only and Windows keeps the Go
//! transport. The build fails here on purpose: a binary that cannot reach
//! the daemon must not be shipped by accident.
#![cfg_attr(windows, allow(unused))]

//!
//! The binary in `main.rs` is the program an agent runs. The modules are
//! public so the contract test can compute the same socket path the hook
//! opens, and so the path rules can be held against the Go implementation.

#[cfg(windows)]
compile_error!(
    "The Rust hook transport does not support Windows yet: std::os::windows::net::UnixStream \
     is still unstable. Windows keeps the Go transport."
);

pub mod agent;
pub mod ipc;
pub mod protocol;
pub mod sockpath;
pub mod spawn;
