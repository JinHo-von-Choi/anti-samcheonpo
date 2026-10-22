//! The wire contract with the daemon.
//!
//! The field names and the framing are fixed by the Go daemon
//! (`internal/live/daemon.go`). A change here is a protocol change and must
//! be made on both sides at once.

use serde::{Deserialize, Serialize};
use serde_json::value::RawValue;

/// The request frame the hook sends for one event.
#[derive(Serialize)]
pub struct Request<'a> {
    pub v: u8,
    pub agent: &'a str,
    pub event: &'a str,
    /// Absolute deadline in unix milliseconds. The daemon stops its own work
    /// at this point instead of answering late.
    pub deadline_unix_ms: i64,
    /// The original hook payload, forwarded unchanged.
    pub payload: &'a RawValue,
}

/// The response frame the daemon sends back.
#[derive(Deserialize, Default)]
pub struct Response {
    /// Absent when the daemon has nothing to say, `null` for an empty
    /// decision.
    #[serde(default)]
    pub output: Option<Box<RawValue>>,
    #[serde(default)]
    pub error: String,
    /// A one-time token. The client returns it after the output is printed.
    #[serde(default)]
    pub delivery_token: String,
}

impl Response {
    /// True when the daemon answered with something to print.
    pub fn has_output(&self) -> bool {
        match &self.output {
            None => false,
            Some(raw) => {
                let text = raw.get().trim();
                !text.is_empty() && text != "null"
            }
        }
    }
}

/// The one-way receipt the client returns after printing the output.
#[derive(Serialize)]
pub struct Ack<'a> {
    pub delivery_token: &'a str,
    pub printed: bool,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn empty_output_is_not_printable() {
        let r: Response = serde_json::from_str(r#"{"output":null}"#).unwrap();
        assert!(!r.has_output());
        let r: Response = serde_json::from_str(r#"{}"#).unwrap();
        assert!(!r.has_output());
        let r: Response = serde_json::from_str(r#"{"output":{"a":1}}"#).unwrap();
        assert!(r.has_output());
    }

    #[test]
    fn request_field_names_are_fixed() {
        let payload: Box<RawValue> = serde_json::from_str(r#"{"x":1}"#).unwrap();
        let req = Request {
            v: 1,
            agent: "claude",
            event: "PreToolUse",
            deadline_unix_ms: 5,
            payload: &payload,
        };
        let text = serde_json::to_string(&req).unwrap();
        assert!(text.contains(r#""v":1"#));
        assert!(text.contains(r#""agent":"claude""#));
        assert!(text.contains(r#""event":"PreToolUse""#));
        assert!(text.contains(r#""deadline_unix_ms":5"#));
        assert!(text.contains(r#""payload":{"x":1}"#));
    }
}
