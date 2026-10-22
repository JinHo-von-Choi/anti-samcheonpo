//! Socket path parity.
//!
//! The Go daemon listens on the path the transport computes. Both
//! implementations read the same vectors, so a divergence in the rules shows
//! up as a failing test rather than as a hook that cannot reach the daemon.

use serde::Deserialize;

const VECTORS: &str = concat!(
    env!("CARGO_MANIFEST_DIR"),
    "/../../internal/sockpath/vectors.json"
);

#[derive(Deserialize)]
struct Spec {
    #[allow(dead_code)]
    version: u32,
    vectors: Vec<Vector>,
}

#[derive(Deserialize)]
struct Vector {
    home: String,
    tmp: String,
    user: String,
    expect: String,
}

#[test]
fn the_socket_path_matches_the_shared_vectors() {
    let raw = std::fs::read_to_string(VECTORS).expect("벡터 읽기 실패");
    let spec: Spec = serde_json::from_str(&raw).expect("벡터 해석 실패");
    assert!(!spec.vectors.is_empty(), "벡터가 없다");
    for v in &spec.vectors {
        let got = samcheonpo_hook::sockpath::for_home(&v.home, &v.tmp, &v.user);
        assert_eq!(
            got, v.expect,
            "home={} tmp={} user={}",
            v.home, v.tmp, v.user
        );
    }
}
