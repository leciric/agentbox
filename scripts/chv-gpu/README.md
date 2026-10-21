# AgentBox's Cloud Hypervisor, and the Linux VM's GPU

The Cloud Hypervisor every Linux VM runs: upstream's v53.0 with Spectrum OS's virtio-gpu patchset
rebased onto it ([`patches/`](patches/README.md)), and beside it crosvm's GPU device and
virglrenderer, which give the VM the host's GPU when "GPU for agents" is on
(`internal/hostvm/chv/gpu.go`). `build.sh` makes the tarball, the Cloud Hypervisor workflow
(`.github/workflows/chv.yml`) builds and publishes it as the release
`chv-<version>-<hash of its inputs>` (`publish.sh`) and pins it in `internal/hostvm/chv/tools.go` (`chvBundle`, through a pull request `pin.sh` makes)
downloads it by its SHA256: nobody compiles anything. `try.sh`, from the spike that started it
(#427), boots a throwaway guest with the tarball on a machine with KVM and a GPU and says what
the guest got.

## The pieces

```
guest: Mesa virgl (OpenGL), Mesa Venus (Vulkan)
   │  virtio-gpu (3D, blob resources, host-visible memory)
cloud-hypervisor + Spectrum's GPU patchset    ← the VMM: a vhost-user front end for virtio-gpu
   │  vhost-user, plus a shared-memory region for blobs
crosvm device gpu                             ← the device, a separate host process
   │  rutabaga
virglrenderer (GL contexts; Venus contexts in virgl_render_server)
   │
host Mesa / GPU driver on /dev/dri/renderD128
```

- **Cloud Hypervisor** has no GPU device of its own. [Spectrum OS](https://spectrum-os.org/software/cloud-hypervisor/)
  keeps a patchset adding one: `--gpu socket=…`, a vhost-user frontend for crosvm's GPU backend
  (not QEMU's `vhost-user-gpu` protocol), with patches to rust-vmm's `vhost` crates for the shared
  memory region that host-visible blobs (and so Venus) need. Their newest, `50.0-spectrum0`,
  targets Cloud Hypervisor v50.0; [`patches/`](patches/README.md) has it rebased onto v53.0, and
  says how, and what the next version costs. Without `--gpu` none of it runs: the binary takes
  AgentBox's VM's command line exactly as upstream's v53.0 does (`test/chv_args_test.sh`).
- **crosvm's `device gpu`** is crosvm's virtio-gpu device as a standalone vhost-user backend. It
  has no releases; the commit is the one Spectrum tested the patchset with.
- **virglrenderer** turns the guest's virgl (OpenGL) and Venus (Vulkan) command streams into host
  GL and Vulkan calls. Built with Venus and its render server (`libexec/virgl_render_server`,
  which crosvm starts for Venus contexts).

## Versions

All pinned in [`versions.env`](versions.env):

| | version | licence |
|---|---|---|
| Cloud Hypervisor | v53.0, static (musl, `--features mshv`, as upstream's release) | Apache-2.0 AND BSD-3-Clause |
| Spectrum OS GPU patchset | 50.0-spectrum0, rebased onto v53.0 ([`patches/`](patches/README.md)) | Apache-2.0 AND BSD-3-Clause (Google) |
| rust-vmm vhost, vhost-user-backend | vhost-user-backend-v0.22.0 (vhost 0.16.0), patched | Apache-2.0 OR BSD-3-Clause |
| crosvm | 18bc84d0 (2025-11-24), default features + `virgl_renderer` | BSD-3-Clause |
| minijail (linked into crosvm) | bfd22f25 | BSD-3-Clause |
| virglrenderer | 1.2.0, `-Dvenus=true -Drender-server=true` | MIT |
| Rust | 1.88.0 (crosvm's pin), 1.89.0 (Cloud Hypervisor v53's minimum) | |
| build system | Debian 12, packages from snapshot.debian.org 2026-10-15 | |

The Rust crates linked in are almost all MIT and/or Apache-2.0; nothing copyleft is linked. The
tarball carries each project's licence files under `licenses/` and Spectrum's patches as applied.

try.sh downloads, on the machine it runs on and not into the tarball: Debian 13's generic cloud
image `20260914-2601` (the build AgentBox's VM uses; Debian's own licences), Cloud Hypervisor's
EDK2 firmware `ch-97eeb7b09`, AgentBox's VM's (BSD-2-Clause-Patent), and passt (GPL-2.0-or-later)
when the host has none.

## build.sh

```bash
scripts/chv-gpu/build.sh [OUTPUT_DIR]     # default scripts/chv-gpu/out/
```

Builds in Docker (BuildKit), linux/amd64 only, in about 15 minutes on 4 cores.
`CARGO_BUILD_JOBS` (default 4) bounds the Rust builds: Cloud Hypervisor links with LTO. Out come
`chv-53.0-<hash>-x86_64.tar.gz`, its `.sha256`, `VERSIONS` and `HOST-LIBRARIES`, where `<hash>` is
`inputs-hash.sh`'s, of every file the build reads. Change any, and main's next run publishes a new
release, since a published build is never replaced, and opens a pull request pinning it (crosvm
doesn't build byte for byte alike twice, so the pin follows what's published, not a local build).

```
bin/cloud-hypervisor, bin/ch-remote   static; says v53.0.0-agentbox-<hash>
bin/crosvm                            glibc ≥ 2.36, RUNPATH $ORIGIN/../lib
libexec/virgl_render_server
lib/libvirglrenderer.so.1             bundled: distributions' builds often lack Venus
licenses/, VERSIONS, HOST-LIBRARIES, SHA256SUMS
```

**What the host must provide** (`HOST-LIBRARIES` lists the exact sonames): glibc 2.36 or newer
(Debian 12, Ubuntu 22.10, Fedora 37 and later), and the graphics stack it renders with:
`libepoxy`, `libdrm`, `libgbm`, `libvulkan` (the loader), `libEGL`/`libGL` through libepoxy,
`libwayland-client`, `libX11`, `libcap`. These aren't bundled on purpose: they must be the host's
own, matching its Mesa and its GPU driver. Any desktop Linux with Mesa installed has them.

Git sources go through [`fetch-git.sh`](fetch-git.sh): five tries per URL, 2 to 16 seconds
apart, since chromium.googlesource.com answers 503 now and then, and then Google's GitHub mirrors
of crosvm and minijail. Whichever serves it, the commit must be the pinned one.

Reproducibility: every source is pinned by commit or SHA-256, Debian's packages by snapshot date,
the Rust images by digest; paths are remapped, file times set to `SOURCE_DATE_EPOCH`, and the
tarball written in a fixed order with fixed owners. A build and a `DOCKER_BUILD_FLAGS=--no-cache`
rebuild on one machine gave the same tarball, byte for byte, but that doesn't hold across machines:
CI's crosvm and its cloud-hypervisor binary both hashed differently from an agent's build of the
same inputs. So the claim is only same-machine, and the pin follows what CI published.

**arm64** isn't built. It would build the same way on an arm64 host (with
`aarch64-unknown-linux-musl`), but cross-building crosvm and virglrenderer needs an arm64 sysroot
of the graphics libraries, and AgentBox's Linux VM is amd64-only today anyway.

## try.sh

```bash
scripts/chv-gpu/try.sh --check            # prerequisites only
scripts/chv-gpu/try.sh                    # the run: 5 to 15 minutes, mostly apt in the guest
scripts/chv-gpu/try.sh --keep             # and keep the serial log, disk and process logs
```

Runs as your user: no root, no tap device, no firewall rules. It needs `/dev/kvm` and the render
node readable and writable by you (the `kvm` and `render` groups), `curl`, `tar`, `xz`, and one
of `cloud-localds`, `genisoimage`, `mkisofs`, `xorriso` or `mkfs.vfat`+`mcopy` for the cloud-init
seed. It then:

1. checks those, and reports the render node's driver and PCI id, the host's Mesa and
   virglrenderer packages, and its own Vulkan device;
2. starts passt (the guest's network, as in AgentBox's VM), crosvm's GPU device on the render node
   with GL (virgl) and Vulkan (Venus) contexts and no window (crosvm's stub display), and Cloud
   Hypervisor with `--gpu`, a copy of Debian 13's generic image (its kernel has `virtio_gpu`; the
   cloud kernel AgentBox's VM uses doesn't) and a cloud-init seed;
3. in the guest, cloud-init installs Mesa's drivers, `mesa-utils`, `vulkan-tools`, glmark2, vkmark
   and Xorg, then [`guest/run-tests.sh`](guest/run-tests.sh) runs `glxinfo -B`,
   `eglinfo -B`, `vulkaninfo --summary`, ~20 s of glmark2 and ~20 s of vkmark on the GPU, and then
   `glxinfo`, `vulkaninfo`, glmark2 and vkmark again with `LIBGL_ALWAYS_SOFTWARE=1` and only
   lavapipe, prints it all on the serial console and powers off;
4. prints one summary block to paste back: host, guest, renderers, scores with the GPU and in
   software, and what went wrong.

Every process it starts runs in its own session and is stopped when it exits, Ctrl-C included,
and its work directory (`~/.cache/agentbox-chv-gpu/run.*`) is removed unless `--keep`. Downloads
stay cached in `~/.cache/agentbox-chv-gpu`; delete it to free 1.5 GB. AgentBox's own VM, its
files, sockets and processes aren't touched, and it can run at the same time.

## Tests

```bash
scripts/chv-gpu/test/parse_test.sh        # the parsing and the summary, against fixtures/
scripts/chv-gpu/test/chv_args_test.sh     # the tarball's binaries on try.sh's and AgentBox's command lines
scripts/chv-gpu/test/seed_test.sh         # the cloud-init seed, with each tool installed
scripts/chv-gpu/test/guest-in-qemu.sh     # the guest half under QEMU, no GPU or KVM needed
```

`chv_args_test.sh` proves the tarball's Cloud Hypervisor parses and validates the command lines
try.sh (`chv_command`) and AgentBox's VM (`chArgs`, with the GPU and without, written by the Go
test `TestWriteCHArgs`) give it, with no KVM, and that upstream's v53.0 says exactly the same of
AgentBox's line without the GPU. Cloud Hypervisor opens `/dev/kvm` and checks its API version
before it reads its options, so the test runs it in a user and mount namespace where `/dev/kvm` is
an empty file, under [`fake-kvm`](test/fake-kvm.c). That ptrace supervisor answers
`KVM_GET_API_VERSION` and lets every other KVM ioctl fail. The first one after parsing, as the VM
is created, ends the run and proves the options got through. The test also checks that the harness
catches an unknown option and vhost-user without shared memory, and that crosvm takes try.sh's
and AgentBox's `--params`. It needs `gcc`, `go` and unprivileged user namespaces.

`guest-in-qemu.sh` boots the same image, seed and guest script under QEMU with its 2D virtio GPU:
cloud-init, the packages, Xorg, the framing and the summary are checked; the GPU runs fall back to
llvmpipe or fail, as they should with no 3D.

## Known not to work, or not tried

- **NVIDIA's proprietary driver and Venus**: Venus needs the host's Vulkan driver to export and
  import dma-bufs (`VK_EXT_external_memory_dma_buf`, `VK_EXT_image_drm_format_modifier`), which
  NVIDIA's driver doesn't support, so Vulkan in the guest fails. virgl (OpenGL) goes through
  EGL/GBM, which NVIDIA supports from driver 495; it's untested. Nouveau/NVK is untested too.
- **Blobs must be fds**: Spectrum's patchset handles only `SHMEM_MAP` from crosvm (map this fd
  into the guest), not `EXTERNAL_MAP` (a pointer into crosvm's memory) or `GPU_MAP` (Vulkan
  memory by device UUID). So try.sh turns on crosvm's `external-blob`, and virglrenderer is built
  with `minigbm_allocation`: GL buffers the guest maps are GBM buffers exported as dma-bufs (the
  host's GL needs `GL_EXT_memory_object_fd`), and Venus's are dma-bufs when the host's Vulkan
  driver exports them (RADV and ANV do). A blob that can't be exported fails to map in the guest
  and the summary counts it; without `external-blob`, the first such blob (resource 23 on an RX
  6750 XT) made Cloud Hypervisor drop the device and crosvm panic.
- **Which GPU**: `--render-node` (default `renderD128`; `--check` lists them) picks the host GPU,
  and AgentBox picks a discrete GPU before one on the CPU (`pickRenderNode`;
  `AGENTBOX_VM_RENDER_NODE=renderD129` in the environment `agentbox vm start` runs in picks another).
  - **GL:** our crosvm patch's `render-node` passes the node to rutabaga as a GPU path, and
    rutabaga (also patched: 0.1.75 rejected every render node, because `Path::starts_with` compares
    whole components) hands virglrenderer its fd. With `surfaceless` off, EGL comes from the GBM
    device on that node. crosvm.log says `virglrenderer: using GPU path "/dev/dri/renderD…"`, and
    the summary repeats it.
  - **Vulkan:** Venus lists every host GPU, and the guest puts the chosen one first (Mesa's
    device-select layer, in the guest; AgentBox's agents get `MESA_VK_DEVICE_SELECT` in their
    profile from what the supervisor tells the VM).
  - `--isolate-gpu` (try.sh only) also runs crosvm where `/dev/dri` holds only that node. It's an
    experiment: on an RX 6750 XT, vrend's winsys failed to initialise there and GL and Venus both
    fell back to software.
  - **Not** Mesa's device-select layer on the host: in the render server it breaks Venus (vkr
    fails `vkEnumeratePhysicalDeviceGroups` with `ERROR_INITIALIZATION_FAILED`). Nor `DRI_PRIME`:
    virglrenderer's node choice ignores it, and that same layer reads it. The guest still puts the
    chosen GPU first with the layer, in the guest, where it's harmless.
  - **The summary** says where each ran: GL from the render node crosvm had open (by device number), Vulkan from the
    vendor:device vkmark used. It flags either one not on the chosen node.
- **No display**: crosvm runs with its stub display, so the guest has a scanout (its X server
  works) but nothing is shown on the host. AgentBox's agents draw on their own virtual displays,
  so it's what we'd want, but crosvm's Wayland/X display paths aren't exercised.
- **virgl's OpenGL** tops out at what virglrenderer translates (OpenGL 4.3 / GLES 3.2 on most
  hosts) and costs a copy per frame; Venus is near native but needs a guest kernel 5.16 or later
  with blob resources and context init (Debian 13's 6.12 has them).
- **Spectrum's patchset** was written for crosvm's cross-domain (Wayland) contexts, Spectrum's
  use; virgl and Venus over it ran on the user's RX 6750 XT with the v50 build. Snapshot, restore
  and live migration of a VM with the GPU device weren't tried, nor was `ch-remote add-gpu`
  (AgentBox gives the device only as the VM starts).
- **amd64 only**, see build.sh above.
