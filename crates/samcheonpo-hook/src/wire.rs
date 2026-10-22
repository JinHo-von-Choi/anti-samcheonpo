//! The request frame, byte for byte.
//!
//! The Go transport builds the frame with `json.Marshal` over a map, so the
//! bytes on the wire are fixed by the Go rules: the keys are sorted, the
//! payload is compacted, and `<`, `>`, `&`, U+2028, and U+2029 are escaped.
//! The daemon reads JSON, so any of these choices would work on their own.
//! The contract is stricter: the two transports must put the same bytes on
//! the wire, and a frame that differs in one byte is a frame the other
//! transport did not write. This module reproduces the Go rules exactly.
//!
//! The Go side is the reference. `internal/hookclient/hookclient.go:173`
//! marshals the request from a map with a `json.RawMessage` payload, and
//! `encoding/json` sorts map keys, compacts raw messages, and escapes HTML.

/// The nesting limit of the Go scanner (`encoding/json`, `maxNestingDepth`).
const MAX_DEPTH: usize = 10_000;

/// Escapes a string the way `json.Marshal` escapes a Go string.
///
/// Go escapes `<`, `>`, and `&` so a JSON document can be embedded in HTML,
/// and it escapes U+2028 and U+2029 so a JSON document can be embedded in
/// JavaScript. A Rust string is always valid UTF-8, so the invalid byte case
/// of the Go encoder does not exist here.
pub fn json_string(s: &str) -> String {
    let mut out = String::with_capacity(s.len() + 2);
    out.push('"');
    for c in s.chars() {
        match c {
            '"' => out.push_str("\\\""),
            '\\' => out.push_str("\\\\"),
            '\n' => out.push_str("\\n"),
            '\r' => out.push_str("\\r"),
            '\t' => out.push_str("\\t"),
            '\u{8}' => out.push_str("\\b"),
            '\u{c}' => out.push_str("\\f"),
            '<' => out.push_str("\\u003c"),
            '>' => out.push_str("\\u003e"),
            '&' => out.push_str("\\u0026"),
            '\u{2028}' => out.push_str("\\u2028"),
            '\u{2029}' => out.push_str("\\u2029"),
            c if (c as u32) < 0x20 => {
                out.push_str(&format!("\\u{:04x}", c as u32));
            }
            c => out.push(c),
        }
    }
    out.push('"');
    out
}

/// What the scanner expects next.
#[derive(Clone, Copy, PartialEq, Eq)]
enum Mode {
    /// A value must follow.
    Value,
    /// A value or the end of an array may follow.
    ValueOrEnd,
    /// A key or the end of an object may follow.
    KeyOrEnd,
    /// A key must follow.
    Key,
    /// A colon must follow.
    Colon,
    /// A comma or the end of an object may follow.
    CommaOrEndObj,
    /// A comma or the end of an array may follow.
    CommaOrEndArr,
    /// The document is complete.
    Done,
}

/// Compacts and escapes a JSON document the way `json.Marshal` compacts a
/// `json.RawMessage`.
///
/// Returns `None` when the document is not valid JSON. The Go scanner
/// accepts any byte but `"` and `\` inside a string, so invalid UTF-8 is
/// valid JSON for Go and is kept here as well.
pub fn compact(raw: &[u8]) -> Option<Vec<u8>> {
    let mut out: Vec<u8> = Vec::with_capacity(raw.len());
    // `true` marks an object, `false` an array.
    let mut stack: Vec<bool> = Vec::new();
    let mut i = 0usize;
    let mut mode = Mode::Value;

    loop {
        match mode {
            Mode::Value | Mode::ValueOrEnd => {
                skip_space(raw, &mut i);
                let b = *raw.get(i)?;
                if b == b']' && mode == Mode::ValueOrEnd {
                    // The array is empty.
                    out.push(b']');
                    stack.pop();
                    i += 1;
                    mode = after_value(&stack);
                    continue;
                }
                match b {
                    b'{' => {
                        if stack.len() >= MAX_DEPTH {
                            return None;
                        }
                        out.push(b'{');
                        stack.push(true);
                        mode = Mode::KeyOrEnd;
                        i += 1;
                    }
                    b'[' => {
                        if stack.len() >= MAX_DEPTH {
                            return None;
                        }
                        out.push(b'[');
                        stack.push(false);
                        mode = Mode::ValueOrEnd;
                        i += 1;
                    }
                    b'"' => {
                        i = copy_string(raw, i, &mut out)?;
                        mode = after_value(&stack);
                    }
                    b't' => {
                        copy_literal(raw, &mut i, b"true", &mut out)?;
                        mode = after_value(&stack);
                    }
                    b'f' => {
                        copy_literal(raw, &mut i, b"false", &mut out)?;
                        mode = after_value(&stack);
                    }
                    b'n' => {
                        copy_literal(raw, &mut i, b"null", &mut out)?;
                        mode = after_value(&stack);
                    }
                    b'-' | b'0'..=b'9' => {
                        i = copy_number(raw, i, &mut out)?;
                        mode = after_value(&stack);
                    }
                    _ => return None,
                }
            }
            Mode::KeyOrEnd => {
                skip_space(raw, &mut i);
                if raw.get(i) == Some(&b'}') {
                    out.push(b'}');
                    stack.pop();
                    i += 1;
                    mode = after_value(&stack);
                } else {
                    i = copy_string(raw, i, &mut out)?;
                    mode = Mode::Colon;
                }
            }
            Mode::Key => {
                skip_space(raw, &mut i);
                i = copy_string(raw, i, &mut out)?;
                mode = Mode::Colon;
            }
            Mode::Colon => {
                skip_space(raw, &mut i);
                if raw.get(i) != Some(&b':') {
                    return None;
                }
                out.push(b':');
                i += 1;
                mode = Mode::Value;
            }
            Mode::CommaOrEndObj => {
                skip_space(raw, &mut i);
                match raw.get(i) {
                    Some(b',') => {
                        out.push(b',');
                        i += 1;
                        mode = Mode::Key;
                    }
                    Some(b'}') => {
                        out.push(b'}');
                        stack.pop();
                        i += 1;
                        mode = after_value(&stack);
                    }
                    _ => return None,
                }
            }
            Mode::CommaOrEndArr => {
                skip_space(raw, &mut i);
                match raw.get(i) {
                    Some(b',') => {
                        out.push(b',');
                        i += 1;
                        mode = Mode::Value;
                    }
                    Some(b']') => {
                        out.push(b']');
                        stack.pop();
                        i += 1;
                        mode = after_value(&stack);
                    }
                    _ => return None,
                }
            }
            Mode::Done => {
                skip_space(raw, &mut i);
                if i != raw.len() {
                    return None;
                }
                return Some(out);
            }
        }
    }
}

/// The mode that follows a finished value.
fn after_value(stack: &[bool]) -> Mode {
    match stack.last() {
        None => Mode::Done,
        Some(true) => Mode::CommaOrEndObj,
        Some(false) => Mode::CommaOrEndArr,
    }
}

/// Skips the whitespace the JSON grammar allows.
fn skip_space(raw: &[u8], i: &mut usize) {
    while let Some(b) = raw.get(*i) {
        match b {
            b' ' | b'\t' | b'\n' | b'\r' => *i += 1,
            _ => break,
        }
    }
}

/// Copies a string, escaping what the Go encoder escapes.
///
/// The escape text of the source is kept as it is: `\u00e9` stays `\u00e9`,
/// because the Go encoder compacts a raw message without re-encoding it.
fn copy_string(raw: &[u8], start: usize, out: &mut Vec<u8>) -> Option<usize> {
    let mut i = start + 1;
    out.push(b'"');
    loop {
        let b = *raw.get(i)?;
        match b {
            b'"' => {
                out.push(b'"');
                return Some(i + 1);
            }
            b'\\' => {
                let e = *raw.get(i + 1)?;
                match e {
                    b'"' | b'\\' | b'/' | b'b' | b'f' | b'n' | b'r' | b't' => {
                        out.push(b'\\');
                        out.push(e);
                        i += 2;
                    }
                    b'u' => {
                        let hex = raw.get(i + 2..i + 6)?;
                        if !hex.iter().all(|c| c.is_ascii_hexdigit()) {
                            return None;
                        }
                        out.push(b'\\');
                        out.push(b'u');
                        out.extend_from_slice(hex);
                        i += 6;
                    }
                    _ => return None,
                }
            }
            b'<' => {
                out.extend_from_slice(b"\\u003c");
                i += 1;
            }
            b'>' => {
                out.extend_from_slice(b"\\u003e");
                i += 1;
            }
            b'&' => {
                out.extend_from_slice(b"\\u0026");
                i += 1;
            }
            0xE2 => {
                // U+2028 and U+2029 are line separators in JavaScript.
                if raw.get(i + 1) == Some(&0x80)
                    && matches!(raw.get(i + 2), Some(0xA8) | Some(0xA9))
                {
                    let last = raw[i + 2];
                    out.extend_from_slice(if last == 0xA8 { b"\\u2028" } else { b"\\u2029" });
                    i += 3;
                } else {
                    out.push(b);
                    i += 1;
                }
            }
            b if b < 0x20 => return None,
            b => {
                out.push(b);
                i += 1;
            }
        }
    }
}

/// Copies one of the three literals.
fn copy_literal(raw: &[u8], i: &mut usize, word: &[u8], out: &mut Vec<u8>) -> Option<()> {
    if raw.get(*i..*i + word.len())? != word {
        return None;
    }
    out.extend_from_slice(word);
    *i += word.len();
    Some(())
}

/// Copies a number. The literal text is kept, so `1.50` stays `1.50` and
/// `1e3` stays `1e3`, which is what the Go encoder does with a raw message.
fn copy_number(raw: &[u8], start: usize, out: &mut Vec<u8>) -> Option<usize> {
    let mut i = start;
    if raw.get(i) == Some(&b'-') {
        out.push(b'-');
        i += 1;
    }
    match raw.get(i)? {
        b'0' => {
            out.push(b'0');
            i += 1;
        }
        b'1'..=b'9' => {
            while let Some(c @ b'0'..=b'9') = raw.get(i) {
                out.push(*c);
                i += 1;
            }
        }
        _ => return None,
    }
    if raw.get(i) == Some(&b'.') {
        out.push(b'.');
        i += 1;
        let mut digits = 0;
        while let Some(c @ b'0'..=b'9') = raw.get(i) {
            out.push(*c);
            i += 1;
            digits += 1;
        }
        if digits == 0 {
            return None;
        }
    }
    if matches!(raw.get(i), Some(b'e') | Some(b'E')) {
        out.push(raw[i]);
        i += 1;
        if matches!(raw.get(i), Some(b'+') | Some(b'-')) {
            out.push(raw[i]);
            i += 1;
        }
        let mut digits = 0;
        while let Some(c @ b'0'..=b'9') = raw.get(i) {
            out.push(*c);
            i += 1;
            digits += 1;
        }
        if digits == 0 {
            return None;
        }
    }
    Some(i)
}

/// Builds the request frame.
///
/// The key order is the sorted order of the Go map: `agent`,
/// `deadline_unix_ms`, `event`, `payload`, `v`.
///
/// `compacted_payload` must come from [`compact`]. The Go transport puts the
/// compacted payload on the wire, and a payload that keeps its whitespace or
/// its unescaped HTML characters is a different frame.
pub fn request_frame(
    agent: &str,
    event: &str,
    deadline_unix_ms: i64,
    compacted_payload: &[u8],
) -> Vec<u8> {
    let mut out = Vec::with_capacity(compacted_payload.len() + 96);
    out.extend_from_slice(b"{\"agent\":");
    out.extend_from_slice(json_string(agent).as_bytes());
    out.extend_from_slice(b",\"deadline_unix_ms\":");
    out.extend_from_slice(deadline_unix_ms.to_string().as_bytes());
    out.extend_from_slice(b",\"event\":");
    out.extend_from_slice(json_string(event).as_bytes());
    out.extend_from_slice(b",\"payload\":");
    out.extend_from_slice(compacted_payload);
    out.extend_from_slice(b",\"v\":1}");
    out
}

/// Builds the delivery receipt the client returns after printing the output.
pub fn ack_frame(delivery_token: &str, printed: bool) -> Vec<u8> {
    let mut out = Vec::with_capacity(delivery_token.len() + 40);
    out.extend_from_slice(b"{\"delivery_token\":");
    out.extend_from_slice(json_string(delivery_token).as_bytes());
    out.extend_from_slice(b",\"printed\":");
    out.extend_from_slice(if printed { b"true" } else { b"false" });
    out.push(b'}');
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    fn compacted(raw: &str) -> String {
        String::from_utf8(compact(raw.as_bytes()).expect("유효한 JSON이어야 한다")).unwrap()
    }

    #[test]
    fn the_key_order_is_the_sorted_go_map_order() {
        let payload = compact(br#"{"session_id":"contract"}"#).unwrap();
        let frame = request_frame("claude", "PreToolUse", 123, &payload);
        assert_eq!(
            String::from_utf8(frame).unwrap(),
            r#"{"agent":"claude","deadline_unix_ms":123,"event":"PreToolUse","payload":{"session_id":"contract"},"v":1}"#
        );
    }

    #[test]
    fn the_payload_is_compacted() {
        assert_eq!(compacted(r#"{ "b" : 1 , "a" : 2 }"#), r#"{"b":1,"a":2}"#);
        assert_eq!(compacted("[]"), "[]");
        assert_eq!(compacted("{}"), "{}");
        assert_eq!(compacted("  1  "), "1");
    }

    #[test]
    fn html_characters_are_escaped() {
        assert_eq!(
            compacted(r#"{"a":"<x>&y"}"#),
            r#"{"a":"\u003cx\u003e\u0026y"}"#
        );
    }

    #[test]
    fn line_separators_are_escaped() {
        assert_eq!(
            compacted("{\"a\":\"\u{2028}\u{2029}\"}"),
            r#"{"a":"\u2028\u2029"}"#
        );
    }

    #[test]
    fn the_number_text_is_kept() {
        assert_eq!(compacted(r#"{"a":1.50,"b":1e3}"#), r#"{"a":1.50,"b":1e3}"#);
    }

    #[test]
    fn the_escape_text_of_the_source_is_kept() {
        assert_eq!(compacted(r#"{"a":"\u00e9"}"#), r#"{"a":"\u00e9"}"#);
    }

    #[test]
    fn invalid_json_is_rejected() {
        assert!(compact(b"{not json").is_none());
        assert!(compact(b"").is_none());
        assert!(compact(b"{\"a\":1,}").is_none());
        assert!(compact(b"[1,2").is_none());
        assert!(compact(b"{\"a\":1} trailing").is_none());
        assert!(compact(b"01").is_none());
        assert!(compact(b"\"\\x\"").is_none());
        assert!(compact(b"\"raw\nnewline\"").is_none());
    }

    #[test]
    fn the_nesting_limit_matches_the_go_scanner() {
        let deep = format!("{}{}", "[".repeat(10_000), "]".repeat(10_000));
        assert!(compact(deep.as_bytes()).is_some());
        let too_deep = format!("{}{}", "[".repeat(10_001), "]".repeat(10_001));
        assert!(compact(too_deep.as_bytes()).is_none());
    }

    #[test]
    fn a_string_is_escaped_the_way_go_escapes_it() {
        assert_eq!(json_string("a\"b\\c"), r#""a\"b\\c""#);
        assert_eq!(json_string("<&>"), r#""\u003c\u0026\u003e""#);
        assert_eq!(json_string("\n\t\r"), r#""\n\t\r""#);
        assert_eq!(json_string("\u{8}\u{c}"), r#""\b\f""#);
        assert_eq!(json_string("\u{1}"), r#""\u0001""#);
        assert_eq!(json_string("\u{7f}"), "\"\u{7f}\"");
    }

    #[test]
    fn the_ack_frame_matches_the_go_map() {
        assert_eq!(
            String::from_utf8(ack_frame("tok-1", true)).unwrap(),
            r#"{"delivery_token":"tok-1","printed":true}"#
        );
    }
}
