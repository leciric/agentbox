//! Requests to the daemon over its unix socket (D15), the way the Electron
//! app's main process relays them (desktop/src/main/connection.ts).
use std::path::{Path, PathBuf};
use std::time::Duration;

use anyhow::{Context as _, Result, anyhow};
use bytes::Bytes;
use http_body_util::{BodyExt, Full};
pub use hyper::Method;
use hyper::Request;
use hyper_util::rt::TokioIo;
use serde::{Serialize, de::DeserializeOwned};
use tokio::sync::mpsc;

use crate::sse;

/// DaemonError is an error the daemon answered with: what it said, and its
/// code (ApiError.code) for the answers a caller acts on.
#[derive(Debug)]
pub struct DaemonError {
    text: String,
    pub message: String,
    pub code: String,
}

impl std::fmt::Display for DaemonError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.text)
    }
}

impl std::error::Error for DaemonError {}

/// code_of is the daemon's code for an error, empty for any other.
pub fn code_of(e: &anyhow::Error) -> &str {
    e.downcast_ref::<DaemonError>()
        .map_or("", |d| d.code.as_str())
}

use crate::types::{ApiError, Event};

/// Client talks to one daemon. Its methods are futures that can be awaited on
/// any executor (GPUI's included): the I/O runs on a tokio runtime of its own.
#[derive(Clone)]
pub struct Client {
    socket: PathBuf,
    rt: tokio::runtime::Handle,
}

/// The daemon's socket: AGENTBOX_SOCKET, else under XDG_DATA_HOME (paths.go).
pub fn default_socket() -> PathBuf {
    if let Ok(s) = std::env::var("AGENTBOX_SOCKET")
        && !s.is_empty()
    {
        return s.into();
    }
    let data = std::env::var("XDG_DATA_HOME")
        .ok()
        .filter(|d| Path::new(d).is_absolute())
        .map(PathBuf::from)
        .unwrap_or_else(|| {
            PathBuf::from(std::env::var("HOME").unwrap_or_default()).join(".local/share")
        });
    data.join("agentbox/run/agentbox.sock")
}

impl Client {
    /// new starts the I/O runtime on a thread of its own.
    pub fn new(socket: impl Into<PathBuf>) -> Self {
        let rt = tokio::runtime::Builder::new_multi_thread()
            .worker_threads(2)
            .thread_name("agentbox-io")
            .enable_all()
            .build()
            .expect("tokio runtime");
        let handle = rt.handle().clone();
        std::thread::spawn(move || rt.block_on(std::future::pending::<()>()));
        Self {
            socket: socket.into(),
            rt: handle,
        }
    }

    pub fn socket(&self) -> &Path {
        &self.socket
    }

    pub(crate) async fn on_rt<T: Send + 'static>(
        &self,
        f: impl Future<Output = Result<T>> + Send + 'static,
    ) -> Result<T> {
        self.rt.spawn(f).await.map_err(|e| anyhow!(e))?
    }

    /// spawn runs a future on the I/O runtime.
    pub fn spawn(&self, f: impl Future<Output = ()> + Send + 'static) {
        self.rt.spawn(f);
    }

    pub async fn get<T: DeserializeOwned + Send + 'static>(&self, path: &str) -> Result<T> {
        let body = self.raw(Method::GET, path, None).await?;
        serde_json::from_slice(&body).with_context(|| format!("GET {path}"))
    }

    pub async fn post<B: Serialize, T: DeserializeOwned + Send + 'static>(
        &self,
        path: &str,
        body: &B,
    ) -> Result<T> {
        let body = self
            .raw(Method::POST, path, Some(serde_json::to_vec(body)?))
            .await?;
        if body.is_empty() {
            return Ok(serde_json::from_slice(b"null")?);
        }
        serde_json::from_slice(&body).with_context(|| format!("POST {path}"))
    }

    /// raw sends one request and returns the body of a 2xx answer; any other is
    /// the daemon's {"error": …}.
    pub async fn raw(&self, method: Method, path: &str, body: Option<Vec<u8>>) -> Result<Bytes> {
        let socket = self.socket.clone();
        let path = path.to_string();
        self.on_rt(async move {
            let mut sender = connect(&socket).await?;
            let req = Request::builder()
                .method(method)
                .uri(&path)
                .header("host", "agentbox")
                .header("content-type", "application/json")
                .body(Full::new(Bytes::from(body.unwrap_or_default())))?;
            let res = sender.send_request(req).await?;
            let status = res.status();
            let headers = res.headers().clone();
            let bytes = res.into_body().collect().await?.to_bytes();
            if let Some(why) = relay_refusal(status, &headers, &bytes) {
                return Err(anyhow!("connecting to the daemon: {why}"));
            }
            if !status.is_success() {
                let (msg, code) = serde_json::from_slice::<ApiError>(&bytes)
                    .map(|e| (e.error, e.code))
                    .unwrap_or_else(|_| {
                        (String::from_utf8_lossy(&bytes).into_owned(), String::new())
                    });
                return Err(DaemonError {
                    text: format!("{path}: {status}: {msg}"),
                    message: msg,
                    code,
                }
                .into());
            }
            Ok(bytes)
        })
        .await
    }

    /// events follows GET /v1/events for as long as the receiver is kept,
    /// reconnecting after a dropped connection. Each (re)connect sends
    /// `None` first, so the caller knows to reload what it shows.
    pub fn events(&self) -> mpsc::UnboundedReceiver<Option<Event>> {
        let (tx, rx) = mpsc::unbounded_channel();
        let socket = self.socket.clone();
        self.rt.spawn(async move {
            loop {
                let _ = follow(&socket, &tx).await;
                if tx.is_closed() {
                    return;
                }
                tokio::time::sleep(Duration::from_secs(1)).await;
            }
        });
        rx
    }
}

type Sender = hyper::client::conn::http1::SendRequest<Full<Bytes>>;

/// dial opens a raw connection to the daemon: its unix socket, or on
/// Windows, where the daemon is in WSL, the named pipe of the relay into it
/// (relay.rs, as desktop/src/main/relay.ts does).
#[cfg(unix)]
pub(crate) async fn dial(socket: &Path) -> Result<tokio::net::UnixStream> {
    tokio::net::UnixStream::connect(socket)
        .await
        .with_context(|| format!("connecting to {}", socket.display()))
}

#[cfg(windows)]
pub(crate) async fn dial(
    socket: &Path,
) -> Result<tokio::net::windows::named_pipe::NamedPipeClient> {
    crate::relay::open(socket).await
}

/// connect opens an HTTP/1 connection to the daemon.
async fn connect(socket: &Path) -> Result<Sender> {
    let stream = dial(socket).await?;
    let (sender, conn) = hyper::client::conn::http1::handshake(TokioIo::new(stream)).await?;
    tokio::spawn(conn);
    Ok(sender)
}

/// relay_refusal is the reason in an answer the relay gave because no daemon
/// listens in the distro, which reads as the daemon not running.
fn relay_refusal(
    status: hyper::StatusCode,
    headers: &hyper::HeaderMap,
    body: &[u8],
) -> Option<String> {
    let header = headers
        .get(crate::relay::RELAY_HEADER)
        .and_then(|v| v.to_str().ok());
    crate::relay::refused(status.as_u16(), header).then(|| {
        serde_json::from_slice::<ApiError>(body)
            .map(|e| e.error)
            .unwrap_or_else(|_| "the AgentBox daemon isn't running".into())
    })
}

async fn follow(socket: &Path, tx: &mpsc::UnboundedSender<Option<Event>>) -> Result<()> {
    let mut sender = connect(socket).await?;
    let req = Request::get("/v1/events")
        .header("host", "agentbox")
        .body(Full::new(Bytes::new()))?;
    let res = sender.send_request(req).await?;
    if !res.status().is_success() {
        let (status, headers) = (res.status(), res.headers().clone());
        let body = res.into_body().collect().await?.to_bytes();
        let why = relay_refusal(status, &headers, &body).unwrap_or_default();
        return Err(anyhow!("events: {status} {why}"));
    }
    tx.send(None)?;
    let mut body = res.into_body();
    let mut parser = sse::Parser::default();
    while let Some(frame) = body.frame().await {
        let Ok(data) = frame?.into_data() else {
            continue;
        };
        for f in parser.push(&data) {
            if let Ok(ev) = serde_json::from_str::<Event>(&f.data) {
                tx.send(Some(ev))?;
            }
        }
    }
    Ok(())
}
