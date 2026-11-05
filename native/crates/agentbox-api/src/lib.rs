//! The daemon's HTTP API over its unix socket, for the native app. A thin
//! client (D15/D19): requests and the event stream, no AgentBox logic.
pub mod client;
pub mod de;
pub mod sse;
pub mod stream;
#[allow(clippy::all, non_camel_case_types)]
pub mod types;

pub use client::Client;
pub use types::*;
