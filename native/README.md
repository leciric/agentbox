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
| `crates/office`       | the Office (#668): its layout, walking, camera and a wgpu renderer, no GPUI  |

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

### Through a VM's socket forward

In VM mode the socket the app opens is forwarded into the VM, where `socat VSOCK-LISTEN:…,fork`
takes each new connection; it queues only a few at once. To try the app against that on a host
daemon, put socat in front of the socket and point the app at it:

```bash
socat UNIX-LISTEN:/t/vm.sock,fork,backlog=1 UNIX-CONNECT:$HOME/.local/share/agentbox/run/agentbox.sock &
AGENTBOX_SOCKET=/t/vm.sock cargo run -p agentbox-api --example burst -- <project> main   # how many requests never got through
AGENTBOX_SOCKET=/t/vm.sock cargo run --release -p agentbox-native
```

### Against a fake daemon

An agent's machine has no Incus, so a real daemon there has a lead but no agents.
`fake-daemon` serves a project's agents from fixtures (changing their state every few seconds, and
streaming a reply to any message) and passes everything else, the event stream included, to a real
daemon:

```bash
cargo run -p fake-daemon -- --listen /t/fake.sock --upstream ~/.local/share/agentbox/run/agentbox.sock
cargo run --release -p agentbox-native -- --socket /t/fake.sock
```

Its agents' Resources views sample made-up numbers every two seconds while the app's presence lease
asks for them (`GET /v1/presence` on the fake shows the leases it holds); the leases go on to the
upstream daemon too.

`--project` (demo), `--agents` (6), `--history` (400 messages per agent) and `--rate` (60 chunks a
second) shape the fixtures. Without `--upstream` it serves one project on its own.

`--neighbours storefront:4,quill:3` adds more projects with a few agents each (no chats), for the
Office's campus.
`--neighbours empty:0` adds one with none. `--icons demo=🦊,quill=logo.svg` gives projects icons (an
emoji, or a picture's file), which the sidebar and the Office's carpets show.

## The Office

Settings → General → Office (off by default) adds the Office to the sidebar: #668's campus of
projects' rooms, where you walk to an agent and open its chat at its desk.
`AGENTBOX_NATIVE_OFFICE=1` opens it at start.

- `crates/office` has no GPUI: the layout, the model, walking and the camera ported from #668's
  TypeScript (with its tests), the rooms, props and robots built in code as #668 builds them, and
  Kenney's models (`assets/kenney`, CC0). It draws with wgpu (the version GPUI uses) offscreen, on a
  thread of its own, and reads each frame back as BGRA. The app (`src/office.rs`) paints the frame
  as an image and puts the name tags, cards and chat over it in GPUI.
- It draws only while something moves or changes: still, it costs no frames and no CPU.
- On a software adapter (lavapipe, llvmpipe, WARP) it draws anyway and says so, with how to fix it.

Measured on lavapipe (no GPU, 3 cores), 1440×813, `AGENTBOX_NATIVE_BENCH=1`: walking about 28
fps (render and readback p50 29 ms), standing still 0 frames and about 1% CPU (the app's own idle).
`cargo run --release -p agentbox-office --example spike` (`OFFICE_SPIKE_BENCH=5`) is the first
spike: one Kenney model, 59 fps painted by GPUI on lavapipe.

- Frames are paced to the display (`Pace` in `engine.rs`): each is drawn for a display refresh, the
  sim steps to exactly that moment, and the view holds it until then; one frame is drawn ahead while
  GPUI shows the last. The bench's `judder` is how far a frame's step in sim time is from its step
  on screen (0 is smooth), `late` how far from its refresh it was painted.
- Depth is reversed (near 1, far 0) so floors stacked a millimetre apart don't fight from the campus
  view; pictures have mips and anisotropic filtering, Kenney's colormap stays nearest.
  `cargo run --release -p agentbox-office --example flicker` counts the pixels that flicker as the
  camera moves 1 mm at a time and writes where, in red.
- A GPU draws with 4× MSAA, a software adapter without; `AGENTBOX_OFFICE_MSAA=1` or `4` says otherwise.

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
