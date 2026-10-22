//! Build-time version injection.
//!
//! The Go CLI and the hook transport must report the same release version.
//! Release builds pass `SAMCHEONPO_VERSION`; a plain `cargo build` falls back
//! to the version the Go tree declares.

fn main() {
    println!("cargo:rerun-if-env-changed=SAMCHEONPO_VERSION");
    let version = std::env::var("SAMCHEONPO_VERSION")
        .ok()
        .filter(|v| !v.trim().is_empty())
        .unwrap_or_else(|| "0.9.6".to_string());
    println!("cargo:rustc-env=SAMCHEONPO_HOOK_VERSION={version}");
}
