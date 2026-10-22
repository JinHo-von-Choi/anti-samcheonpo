//! The wire vectors.
//!
//! `internal/hookclient/wire_vectors.json` holds the Go encoder's own
//! answers: for each input, whether the input is valid JSON and what the
//! payload looks like after the Go encoder compacted it. The Go side checks
//! the same file, so both implementations are held to the same bytes.

use samcheonpo_hook::wire;
use serde::Deserialize;

const VECTORS: &str = concat!(
    env!("CARGO_MANIFEST_DIR"),
    "/../../internal/hookclient/wire_vectors.json"
);

#[derive(Deserialize)]
struct Vector {
    /// The input document, as hex.
    input: String,
    /// Whether the Go scanner accepts the document.
    valid: bool,
    /// The compacted payload, as hex.
    #[serde(default)]
    payload: String,
}

/// Decodes a hex string.
fn decode(text: &str) -> Vec<u8> {
    let bytes = text.as_bytes();
    assert!(bytes.len() % 2 == 0, "16진수 길이가 짝수가 아니다: {text}");
    let mut out = Vec::with_capacity(bytes.len() / 2);
    for pair in bytes.chunks(2) {
        let hi = (pair[0] as char).to_digit(16).expect("16진수가 아니다");
        let lo = (pair[1] as char).to_digit(16).expect("16진수가 아니다");
        out.push((hi * 16 + lo) as u8);
    }
    out
}

/// Encodes bytes as hex.
fn encode(bytes: &[u8]) -> String {
    let mut out = String::with_capacity(bytes.len() * 2);
    for b in bytes {
        out.push_str(&format!("{b:02x}"));
    }
    out
}

#[test]
fn the_wire_vectors_match_the_go_encoder() {
    let raw = std::fs::read_to_string(VECTORS).expect("와이어 벡터 읽기 실패");
    let vectors: Vec<Vector> = serde_json::from_str(&raw).expect("와이어 벡터 해석 실패");
    assert!(!vectors.is_empty(), "와이어 벡터가 없다");

    for v in &vectors {
        let input = decode(&v.input);
        match wire::compact(&input) {
            Some(compacted) => {
                assert!(
                    v.valid,
                    "입력 {}: Go는 거부하는데 Rust는 받아들였다",
                    v.input
                );
                assert_eq!(encode(&compacted), v.payload, "입력 {}: 페이로드", v.input);
            }
            None => assert!(
                !v.valid,
                "입력 {}: Go는 유효하다고 하는데 Rust는 거부했다",
                v.input
            ),
        }
    }
}
