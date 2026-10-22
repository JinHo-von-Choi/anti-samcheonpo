//! The wire contract with the daemon.
//!
//! The field names and the framing are fixed by the Go daemon
//! (`internal/live/daemon.go`). A change here is a protocol change and must
//! be made on both sides at once.

use serde::Deserialize;
use serde_json::value::RawValue;

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
}
