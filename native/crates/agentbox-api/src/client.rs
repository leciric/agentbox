//! Requests to the daemon over its unix socket (D15), the way the Electron
//! app's main process relays them (desktop/src/main/connection.ts).
use std::path::{Path, PathBuf};
use std::sync::{Arc, Mutex};
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
    /// status is the answer's HTTP status.
    pub status: u16,
}

impl std::fmt::Display for DaemonError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.text)
    }
}

impl std::error::Error for DaemonError {}

/// not_ready says a request is worth sending again in a moment, as the
/// Electron app's chat loads do (lib/chat.ts's retryChatReady): no daemon
/// answered it (the connection was refused or dropped), or the daemon said
/// what was asked for isn't there yet (404) or it can't answer now (503).
pub fn not_ready(e: &anyhow::Error) -> bool {
    match e.downcast_ref::<DaemonError>() {
        Some(d) => d.status == 404 || d.status == 503,
        None => unreachable(e),
    }
}

/// unreachable says a request never got an answer from a daemon.
/// That is a connection that failed or closed (an I/O or HTTP error), or the
/// Windows relay finding no daemon; a request that couldn't be made at all
/// (a malformed path) or an answer that couldn't be read is not.
pub fn unreachable(e: &anyhow::Error) -> bool {
    e.downcast_ref::<DaemonError>().is_none()
        && e.chain()
            .any(|c| c.is::<std::io::Error>() || c.is::<hyper::Error>() || c.is::<NoDaemon>())
        && e.chain().all(|c| !c.is::<serde_json::Error>())
}

/// NoDaemon is a connection that reached no daemon, without an I/O error to
/// say so: the relay's refusal, or a new connection closed before a request.
#[derive(Debug)]
struct NoDaemon(String);

impl std::fmt::Display for NoDaemon {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.0)
    }
}

impl std::error::Error for NoDaemon {}

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
    pool: Arc<Pool>,
}

/// Pool keeps the connections requests are sent on, as Node's keep-alive
/// agent does for the Electron app: a request reuses an idle one rather than
/// connecting again. In VM mode every new connection is a vsock connection
/// to a `socat VSOCK-LISTEN` in the VM, which forks for each one and queues
/// only a few at once, so a burst of new connections (opening a project or a
/// lead session asks for a dozen things together) had some of them refused.
/// connecting caps how many new connections are being made at once, for the
/// same reason; a request already on a connection is never held back.
struct Pool {
    idle: Mutex<Vec<Sender>>,
    connecting: tokio::sync::Semaphore,
}

/// At most this many connections made at once, and this many idle ones kept.
const CONNECTING: usize = 4;
const IDLE: usize = 8;

impl Pool {
    fn new() -> Self {
        Self {
            idle: Mutex::new(Vec::new()),
            connecting: tokio::sync::Semaphore::new(CONNECTING),
        }
    }

    fn take(&self) -> Option<Sender> {
        let mut idle = self.idle.lock().unwrap_or_else(|e| e.into_inner());
        while let Some(s) = idle.pop() {
            if !s.is_closed() {
                return Some(s);
            }
        }
        None
    }

    fn put(&self, s: Sender) {
        if s.is_closed() {
            return;
        }
        let mut idle = self.idle.lock().unwrap_or_else(|e| e.into_inner());
        if idle.len() < IDLE {
            idle.push(s);
        }
    }

    /// forget drops every idle connection: one of them was found closed by
    /// the far end (the daemon restarted), so the rest likely are too.
    fn forget(&self) {
        self.idle.lock().unwrap_or_else(|e| e.into_inner()).clear();
    }
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
            pool: Arc::new(Pool::new()),
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
        let (socket, pool) = (self.socket.clone(), self.pool.clone());
        let path = path.to_string();
        self.on_rt(async move {
            let body = Bytes::from(body.unwrap_or_default());
            let (status, headers, bytes) = send(&socket, &pool, method, &path, true, body).await?;
            if let Some(why) = relay_refusal(status, &headers, &bytes) {
                return Err(NoDaemon(format!("connecting to the daemon: {why}")).into());
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
                    status: status.as_u16(),
                }
                .into());
            }
            Ok(bytes)
        })
        .await
    }

    /// fetch GETs a path as it is, whatever its status, with its headers: a
    /// page the daemon serves for a webview (a Hatch page's HTML and its
    /// Content-Security-Policy), as the Electron app's protocol relays it.
    pub async fn fetch(&self, path: &str) -> Result<(u16, Vec<(String, String)>, Bytes)> {
        let (socket, pool) = (self.socket.clone(), self.pool.clone());
        let path = path.to_string();
        self.on_rt(async move {
            let (status, headers, bytes) =
                send(&socket, &pool, Method::GET, &path, false, Bytes::new()).await?;
            let headers = headers
                .iter()
                .filter_map(|(k, v)| Some((k.to_string(), v.to_str().ok()?.to_string())))
                .collect();
            Ok((status.as_u16(), headers, bytes))
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
/// (relay.rs, as desktop/src/main/relay.ts does). A connection refused for a
/// moment is tried again (see redial).
pub(crate) async fn dial(socket: &Path) -> Result<Conn> {
    let mut waits = REDIAL.iter();
    loop {
        match dial_once(socket).await {
            Ok(c) => return Ok(c),
            Err(e) if redial(&e) => match waits.next() {
                Some(ms) => tokio::time::sleep(pause(*ms)).await,
                None => return Err(e),
            },
            Err(e) => return Err(e),
        }
    }
}

/// pause is a wait from REDIAL, give or take a quarter, so requests that
/// were refused together don't all try again at the same moment.
fn pause(ms: u64) -> Duration {
    let nanos = std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_or(0, |d| d.subsec_nanos() as u64);
    let spread = ms / 2 + 1;
    Duration::from_millis(ms - ms / 4 + nanos % spread)
}

/// checkout is a connection to send on: an idle one from the pool, else a
/// new one. Making one takes one of the pool's connecting slots for each try
/// and none while it waits between tries, so a request behind a daemon that
/// isn't there fails after its own few seconds, not after everyone's; and
/// each try looks in the pool first, for a connection another request just
/// gave back.
async fn checkout(socket: &Path, pool: &Pool) -> Result<(Sender, bool)> {
    let mut waits = REDIAL.iter();
    loop {
        if let Some(s) = pool.take() {
            return Ok((s, true));
        }
        let slot = pool.connecting.acquire().await?;
        if let Some(s) = pool.take() {
            return Ok((s, true));
        }
        let res = dial_once(socket).await;
        drop(slot);
        match res {
            Ok(stream) => return Ok((handshake(stream).await?, false)),
            Err(e) if redial(&e) => match waits.next() {
                Some(ms) => tokio::time::sleep(pause(*ms)).await,
                None => return Err(e),
            },
            Err(e) => return Err(e),
        }
    }
}

#[cfg(unix)]
pub(crate) type Conn = tokio::net::UnixStream;
#[cfg(windows)]
pub(crate) type Conn = tokio::net::windows::named_pipe::NamedPipeClient;

#[cfg(unix)]
async fn dial_once(socket: &Path) -> Result<Conn> {
    // The same kind of error, saying where it was connecting to (an error's
    // text is what the app shows).
    tokio::net::UnixStream::connect(socket).await.map_err(|e| {
        let what = format!("connecting to {}: {e}", socket.display());
        anyhow::Error::new(std::io::Error::new(e.kind(), what))
    })
}

#[cfg(windows)]
async fn dial_once(socket: &Path) -> Result<Conn> {
    crate::relay::open(socket).await
}

/// How long to wait before each new try at a connection refused: about 5 s
/// in all, the time Electron's main process gives a daemon it found not
/// listening to answer before it sends the request again (daemon.ts's
/// notListening and ensureDaemon). The app's own connection notice and
/// `agentbox daemon start` (connection.rs) take over from there.
const REDIAL: [u64; 6] = [50, 150, 300, 600, 1200, 2500];

/// redial says whether a failed connect is worth trying again: nobody
/// listening yet or for a moment (a socket being replaced as the daemon or
/// the VM's forward restarts), a listener whose queue is full (EAGAIN on a
/// unix socket), or one reset as it was accepted.
fn redial(e: &anyhow::Error) -> bool {
    use std::io::ErrorKind::*;
    e.chain()
        .filter_map(|c| c.downcast_ref::<std::io::Error>())
        .any(|io| {
            matches!(
                io.kind(),
                ConnectionRefused | NotFound | WouldBlock | ConnectionReset | ConnectionAborted
            )
        })
}

/// connect opens an HTTP/1 connection to the daemon.
async fn connect(socket: &Path) -> Result<Sender> {
    handshake(dial(socket).await?).await
}

async fn handshake(stream: Conn) -> Result<Sender> {
    let (sender, conn) = hyper::client::conn::http1::handshake(TokioIo::new(stream)).await?;
    tokio::spawn(conn);
    Ok(sender)
}

/// unsent says a request failed before any answer came: the connection was
/// closed under it. On a connection reused from the pool that is one the
/// daemon closed while it sat idle (Electron's staleSocket.ts), and on a new
/// one, in VM mode, the VM's forward closing it because nothing in the VM
/// took it.
fn unsent(e: &hyper::Error) -> bool {
    if e.is_incomplete_message() || e.is_canceled() || e.is_closed() {
        return true;
    }
    // Writing it hit a connection already closed: ECONNRESET or EPIPE, as
    // staleSocket.ts has it.
    let mut src = std::error::Error::source(e);
    while let Some(s) = src {
        if let Some(io) = s.downcast_ref::<std::io::Error>() {
            return matches!(
                io.kind(),
                std::io::ErrorKind::BrokenPipe | std::io::ErrorKind::ConnectionReset
            );
        }
        src = s.source();
    }
    false
}

/// idempotent methods may be sent again after a connection closed before
/// their answer, as the daemon may or may not have read them.
fn idempotent(m: &Method) -> bool {
    matches!(
        *m,
        Method::GET | Method::HEAD | Method::PUT | Method::DELETE | Method::OPTIONS
    )
}

/// send sends one request on a pooled connection, or a new one, and reads
/// its whole answer. An idempotent request that fails before any answer is
/// sent again on a new connection (once per stale pooled one, and a few
/// times on new ones); any other only when it never left.
async fn send(
    socket: &Path,
    pool: &Pool,
    method: Method,
    path: &str,
    json: bool,
    body: Bytes,
) -> Result<(hyper::StatusCode, hyper::HeaderMap, Bytes)> {
    let mut fresh_tries = 0;
    loop {
        let (mut sender, reused) = checkout(socket, pool).await?;
        if sender.ready().await.is_err() {
            if reused {
                pool.forget();
                continue;
            }
            return Err(NoDaemon(format!(
                "connecting to {}: the connection closed",
                socket.display()
            ))
            .into());
        }
        let mut req = Request::builder()
            .method(method.clone())
            .uri(path)
            .header("host", "agentbox");
        if json {
            req = req.header("content-type", "application/json");
        }
        let req = req.body(Full::new(body.clone()))?;
        match sender.send_request(req).await {
            Ok(res) => {
                let status = res.status();
                let headers = res.headers().clone();
                let bytes = res.into_body().collect().await?.to_bytes();
                pool.put(sender);
                return Ok((status, headers, bytes));
            }
            // On a reused connection a request the daemon may have read
            // (hyper wrote it, then the connection ended) is sent again only
            // if it's idempotent: a POST could be a message sent twice.
            Err(e) if unsent(&e) && reused && (idempotent(&method) || e.is_canceled()) => {
                pool.forget();
            }
            Err(e) if unsent(&e) && idempotent(&method) && fresh_tries < 3 => {
                fresh_tries += 1;
                tokio::time::sleep(Duration::from_millis(200 * fresh_tries)).await;
            }
            Err(e) => {
                if reused {
                    pool.forget();
                }
                let what = format!("{method} {path}: {e}");
                return Err(anyhow::Error::new(e).context(what));
            }
        }
    }
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

#[cfg(all(test, unix))]
mod tests {
    use super::*;
    use tokio::io::{AsyncReadExt as _, AsyncWriteExt as _};

    /// serve answers each request on a connection with 200 and `body`, and
    /// with `close` ends each connection after one answer.
    async fn serve(ln: tokio::net::UnixListener, close: bool) {
        loop {
            let Ok((mut c, _)) = ln.accept().await else {
                return;
            };
            tokio::spawn(async move {
                let mut buf = vec![0u8; 4096];
                loop {
                    let Ok(n) = c.read(&mut buf).await else {
                        return;
                    };
                    if n == 0 {
                        return;
                    }
                    let res = "HTTP/1.1 200 OK\r\ncontent-length: 2\r\n\r\nok";
                    if c.write_all(res.as_bytes()).await.is_err() || close {
                        return;
                    }
                }
            });
        }
    }

    fn sock(name: &str) -> PathBuf {
        let dir = std::env::temp_dir().join(format!("abapi-{}-{name}", std::process::id()));
        let _ = std::fs::create_dir_all(&dir);
        let p = dir.join("d.sock");
        let _ = std::fs::remove_file(&p);
        p
    }

    #[test]
    fn a_request_waits_for_a_daemon_that_is_starting() {
        let path = sock("starting");
        let client = Client::new(&path);
        let p = path.clone();
        client.spawn(async move {
            tokio::time::sleep(Duration::from_millis(400)).await;
            serve(tokio::net::UnixListener::bind(&p).unwrap(), false).await;
        });
        let rt = tokio::runtime::Runtime::new().unwrap();
        let body = rt
            .block_on(client.raw(Method::GET, "/v1/version", None))
            .unwrap();
        assert_eq!(&body[..], b"ok");
    }

    #[test]
    fn connections_are_kept_and_one_the_daemon_closed_is_replaced() {
        let path = sock("pool");
        let client = Client::new(&path);
        let p = path.clone();
        client.spawn(async move {
            serve(tokio::net::UnixListener::bind(&p).unwrap(), true).await;
        });
        let rt = tokio::runtime::Runtime::new().unwrap();
        rt.block_on(async {
            tokio::time::sleep(Duration::from_millis(100)).await;
            // Each answer closes its connection: the next request, sent on
            // the pooled one, goes again on a new one.
            for _ in 0..5 {
                let body = client.raw(Method::GET, "/v1/x", None).await.unwrap();
                assert_eq!(&body[..], b"ok");
            }
        });
    }

    #[test]
    fn a_burst_of_requests_all_get_answers() {
        let path = sock("burst");
        let client = Client::new(&path);
        let p = path.clone();
        client.spawn(async move {
            serve(tokio::net::UnixListener::bind(&p).unwrap(), false).await;
        });
        let rt = tokio::runtime::Runtime::new().unwrap();
        rt.block_on(async {
            tokio::time::sleep(Duration::from_millis(100)).await;
            let tasks: Vec<_> = (0..64)
                .map(|_| {
                    let c = client.clone();
                    tokio::spawn(async move { c.raw(Method::GET, "/v1/version", None).await })
                })
                .collect();
            for t in tasks {
                assert_eq!(&t.await.unwrap().unwrap()[..], b"ok");
            }
            let idle = client.pool.idle.lock().unwrap().len();
            assert!(idle <= IDLE, "{idle} idle connections kept");
        });
    }

    #[test]
    fn what_is_worth_asking_again() {
        let refused =
            anyhow::Error::new(std::io::Error::from(std::io::ErrorKind::ConnectionRefused))
                .context("connecting to /x.sock: refused");
        assert!(redial(&refused));
        assert!(unreachable(&refused) && not_ready(&refused));
        let gone = |status| -> anyhow::Error {
            DaemonError {
                text: String::new(),
                message: String::new(),
                code: String::new(),
                status,
            }
            .into()
        };
        assert!(not_ready(&gone(404)) && not_ready(&gone(503)));
        assert!(!not_ready(&gone(400)) && !unreachable(&gone(503)));
        assert!(!unreachable(&anyhow!("invalid uri character")));
        let bad =
            anyhow::Error::new(serde_json::from_str::<u8>("x").unwrap_err()).context("GET /v1/x");
        assert!(!unreachable(&bad));
    }
}
