//! The socket transport.
//!
//! One request, one connection, then exit. The transport is a unix domain
//! socket on every supported platform, including Windows 10 1803 and later,
//! which is the same transport the Go hook uses.

use std::io::{Read, Write};
use std::time::{Duration, Instant};

#[cfg(unix)]
pub use std::os::unix::net::UnixStream;
#[cfg(windows)]
pub use std::os::windows::net::UnixStream;

/// The connect timeout for a warm hook.
pub const CONNECT_TIMEOUT: Duration = Duration::from_millis(5);

/// The cold start budget: how long the hook waits for a daemon that is
/// still coming up.
pub const COLD_START_TIMEOUT: Duration = Duration::from_millis(500);

/// The interval between connect retries during the cold start.
pub const COLD_START_INTERVAL: Duration = Duration::from_millis(5);

/// Connects to the daemon socket.
pub fn connect(path: &str, timeout: Duration) -> std::io::Result<UnixStream> {
    let stream = UnixStream::connect(path)?;
    stream.set_read_timeout(Some(timeout))?;
    stream.set_write_timeout(Some(timeout))?;
    Ok(stream)
}

/// Writes one newline terminated frame.
pub fn write_frame(stream: &mut UnixStream, frame: &[u8]) -> std::io::Result<()> {
    stream.write_all(frame)?;
    stream.write_all(b"\n")?;
    stream.flush()
}

/// Reads one newline terminated frame.
///
/// A frame that arrives without a trailing newline before the peer closes
/// the connection is still returned, so a daemon that closes after writing
/// is not mistaken for a timeout. The deadline is absolute: the whole
/// exchange must fit inside it, not every single read.
pub fn read_frame(stream: &mut UnixStream, deadline: Instant) -> std::io::Result<Vec<u8>> {
    let mut buf = Vec::with_capacity(4096);
    let mut byte = [0u8; 1];
    loop {
        let remaining = deadline.saturating_duration_since(Instant::now());
        if remaining.is_zero() {
            return Err(std::io::Error::new(
                std::io::ErrorKind::TimedOut,
                "deadline reached",
            ));
        }
        let _ = stream.set_read_timeout(Some(remaining));
        match stream.read(&mut byte) {
            Ok(0) => {
                if buf.is_empty() {
                    return Err(std::io::Error::new(
                        std::io::ErrorKind::UnexpectedEof,
                        "no frame",
                    ));
                }
                return Ok(buf);
            }
            Ok(_) => {
                if byte[0] == b'\n' {
                    return Ok(buf);
                }
                buf.push(byte[0]);
            }
            Err(e) => return Err(e),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn read_frame_stops_at_the_newline() {
        let (mut client, mut server) = UnixStream::pair().unwrap();
        std::thread::spawn(move || {
            server.write_all(b"{\"a\":1}\n").unwrap();
        });
        let got = read_frame(&mut client, Instant::now() + Duration::from_secs(5)).unwrap();
        assert_eq!(got, b"{\"a\":1}");
    }

    #[test]
    fn read_frame_reports_eof_without_data() {
        let (mut client, server) = UnixStream::pair().unwrap();
        drop(server);
        assert!(read_frame(&mut client, Instant::now() + Duration::from_secs(5)).is_err());
    }
}
