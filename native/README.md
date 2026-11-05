# Native app (proof of concept)

A native desktop client for AgentBox in Rust, drawn with [GPUI](https://www.gpui.rs/) (Zed's UI
framework) and [gpui-component](https://github.com/longbridge/gpui-kit) for widgets. It lives beside
the Electron app in `desktop/`, which it doesn't replace yet.

Like the Electron app it is a thin client of the daemon (D15/D19): it asks over the daemon's unix
socket and shows what comes back, with no AgentBox logic of its own.

| crate                 | what it is                                                                   |
| --------------------- | ---------------------------------------------------------------------------- |
| `crates/agentbox-api` | the API's types (generated from Go), a client for the socket, an SSE parser |
| `crates/app`          | the app, `agentbox-native`                                                   |
| `crates/fake-daemon`  | a stand-in daemon with fixture agents, for where agents can't run            |

## Build and run

Rust comes from rustup (`rustup default stable`). On Linux GPUI needs the X11/Wayland and Vulkan
development packages:

```bash
sudo apt-get install libxkbcommon-x11-dev libwayland-dev libvulkan-dev libfontconfig-dev libfreetype-dev libx11-xcb-dev mesa-vulkan-drivers \
  libavformat-dev libavcodec-dev libswscale-dev libavutil-dev libclang-dev \
  libwebkit2gtk-4.1-dev libgtk-3-dev libasound2-dev
cargo run --release -p agentbox-native                       # against this machine's daemon
cargo run --release -p agentbox-native -- --socket /path.sock # or another socket (also AGENTBOX_SOCKET)
```

Without a GPU, use Mesa's software Vulkan, lavapipe:
`VK_DRIVER_FILES=/usr/share/vulkan/icd.d/lvp_icd.json`. Inside an AgentBox agent, the virtio-GPU
(Venus) device draws a black window; lavapipe works.

### Against a fake daemon

An agent's machine has no Incus, so a real daemon there has a lead but no agents.
`fake-daemon` serves a project's agents from fixtures (changing their state every few seconds, and
streaming a reply to any message) and passes everything else, the event stream included, to a real
daemon:

```bash
cargo run -p fake-daemon -- --listen /t/fake.sock --upstream ~/.local/share/agentbox/run/agentbox.sock
cargo run --release -p agentbox-native -- --socket /t/fake.sock
```

`--project` (demo), `--agents` (6), `--history` (400 messages per agent) and `--rate` (60 chunks a
second) shape the fixtures. Without `--upstream` it serves one project on its own.

## The API's types

`crates/agentbox-api/src/types.rs` is generated from `internal/api` by the same test that writes
the desktop's `api.ts`, from the same list of types:

```bash
UPDATE_TS=1 go test ./internal/api   # writes desktop/src/shared/api.ts and native/…/types.rs
```

`TestRustTypesAreUpToDate` fails when it's stale, as `TestTypeScriptTypesAreUpToDate` does. Every
struct reads Go's JSON leniently: a missing field is its zero value, null is an empty slice or map,
pointers are `Option`, and unknown fields are ignored, so an older app reads a newer daemon.

## Measuring

`AGENTBOX_NATIVE_BENCH=1` prints when the first frame and the data went out, and percentiles of the
time between frames while a chat streams. `AGENTBOX_NATIVE_OPEN=agent-02` opens that agent's chat
at start, and `AGENTBOX_NATIVE_SCROLL_BENCH=3` scrolls it 24 px a frame for 600 frames, three
seconds in, and prints the frame intervals.
