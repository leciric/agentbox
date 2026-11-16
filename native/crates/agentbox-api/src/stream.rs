//! WebSocket streams over the daemon's socket: an agent's terminal, as the
//! Electron app's main process opens them (desktop/src/main/streams.ts).
//! Binary frames carry bytes both ways; text frames carry control messages
//! (a terminal's api::TerminalResize).
use anyhow::{Result, anyhow};
use futures_util::{SinkExt as _, StreamExt as _};
use tokio::sync::mpsc;
use tokio_tungstenite::tungstenite::Message;

use crate::Client;

pub enum Out {
    Bytes(Vec<u8>),
    Text(String),
}

pub enum In {
    Bytes(Vec<u8>),
    /// The stream ended, and why.
    Closed(String),
}

/// Stream is one open WebSocket: send on `tx`, receive on `rx`. Dropping it
/// closes the connection.
pub struct Stream {
    pub tx: mpsc::UnboundedSender<Out>,
    pub rx: mpsc::UnboundedReceiver<In>,
}

impl Client {
    /// stream opens a WebSocket at `path` (with its query).
    pub async fn stream(&self, path: &str) -> Result<Stream> {
        let socket = self.socket().to_path_buf();
        let url = format!("ws://agentbox{path}");
        let ws = self
            .on_rt(async move {
                let conn = crate::client::dial(&socket).await?;
                let (ws, _) =
                    tokio_tungstenite::client_async(url, conn)
                        .await
                        .map_err(|e| match e {
                            tokio_tungstenite::tungstenite::Error::Http(res) => {
                                let body = res.body().as_deref().unwrap_or_default();
                                let msg = serde_json::from_slice::<crate::ApiError>(body)
                                    .map(|e| e.error)
                                    .unwrap_or_else(|_| String::from_utf8_lossy(body).into_owned());
                                anyhow!("{}: {msg}", res.status())
                            }
                            e => anyhow!(e),
                        })?;
                Ok(ws)
            })
            .await?;
        let (out_tx, mut out_rx) = mpsc::unbounded_channel::<Out>();
        let (in_tx, in_rx) = mpsc::unbounded_channel::<In>();
        self.spawn(async move {
            let (mut sink, mut source) = ws.split();
            let writer = async {
                while let Some(m) = out_rx.recv().await {
                    let m = match m {
                        Out::Bytes(b) => Message::Binary(b.into()),
                        Out::Text(t) => Message::Text(t.into()),
                    };
                    if sink.send(m).await.is_err() {
                        break;
                    }
                }
                let _ = sink.close().await;
            };
            let reader = async {
                let why = loop {
                    match source.next().await {
                        Some(Ok(Message::Binary(b))) => {
                            if in_tx.send(In::Bytes(b.to_vec())).is_err() {
                                break String::new();
                            }
                        }
                        Some(Ok(Message::Close(frame))) => {
                            break frame
                                .map(|f| f.reason.to_string())
                                .filter(|r| !r.is_empty())
                                .unwrap_or_else(|| "the session ended".into());
                        }
                        Some(Ok(_)) => {}
                        Some(Err(e)) => break e.to_string(),
                        None => break "the connection closed".into(),
                    }
                };
                let _ = in_tx.send(In::Closed(why));
            };
            tokio::select! {
                _ = writer => {}
                _ = reader => {}
            }
        });
        Ok(Stream {
            tx: out_tx,
            rx: in_rx,
        })
    }
}
