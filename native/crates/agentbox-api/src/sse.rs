//! A server-sent events parser for the daemon's GET /v1/events.

/// One event: its `event:` name and its `data:` lines joined.
#[derive(Debug, Clone, PartialEq)]
pub struct Frame {
    pub event: String,
    pub data: String,
}

/// Parser keeps the partial line and frame between chunks.
#[derive(Default)]
pub struct Parser {
    buf: Vec<u8>,
    event: String,
    data: Vec<String>,
}

impl Parser {
    /// push feeds a chunk and returns the frames it completed.
    pub fn push(&mut self, chunk: &[u8]) -> Vec<Frame> {
        self.buf.extend_from_slice(chunk);
        let mut out = Vec::new();
        while let Some(nl) = self.buf.iter().position(|&b| b == b'\n') {
            let mut line: Vec<u8> = self.buf.drain(..=nl).collect();
            line.pop();
            if line.last() == Some(&b'\r') {
                line.pop();
            }
            let line = String::from_utf8_lossy(&line);
            if line.is_empty() {
                if !self.data.is_empty() || !self.event.is_empty() {
                    out.push(Frame {
                        event: std::mem::take(&mut self.event),
                        data: std::mem::take(&mut self.data).join("\n"),
                    });
                }
            } else if line.starts_with(':') {
                // a comment: ": connected", ": keepalive"
            } else {
                let (field, value) = line.split_once(':').unwrap_or((&line, ""));
                let value = value.strip_prefix(' ').unwrap_or(value);
                match field {
                    "event" => self.event = value.to_string(),
                    "data" => self.data.push(value.to_string()),
                    _ => {}
                }
            }
        }
        out
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn frames_split_across_chunks() {
        let mut p = Parser::default();
        assert!(p.push(b": connected\n\nevent: ag").is_empty());
        let got = p.push(b"ent\ndata: {\"a\":1}\r\n\nevent: chat\ndata: x\ndata: y\n\n");
        assert_eq!(
            got,
            vec![
                Frame {
                    event: "agent".into(),
                    data: "{\"a\":1}".into()
                },
                Frame {
                    event: "chat".into(),
                    data: "x\ny".into()
                },
            ]
        );
    }
}
