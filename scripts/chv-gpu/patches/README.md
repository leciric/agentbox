# Patches

Applied by the Dockerfile in name order, each directory to its project. Every Linux VM runs the
Cloud Hypervisor they make, with "GPU for agents" on or off.

## vhost/: rust-vmm's vhost crates at vhost-user-backend-v0.22.0 (vhost 0.16.0), as Cloud Hypervisor v53.0 uses them

Spectrum OS's `vhost.patch` (50.0-spectrum0), rebased from vhost-user-backend-v0.20.0:

- `0001-vhost_user-add-get_size-to-MsgHeader.patch`: applied as is.
- `0002-vhost-fix-receiving-reply-payloads.patch`: applied as is.
- `0003-vhost_user-add-shared-memory-region-support.patch`: crosvm's non-standard shared-memory
  messages: `GET_SHARED_MEMORY_REGIONS` (front end, 1004), `SHMEM_MAP` and `SHMEM_UNMAP` (back
  end, 1000 and 1001). v0.22 has since standardised its own `SHMEM_MAP`/`SHMEM_UNMAP` (9 and 10,
  with a different body, `VhostUserMMap`) and `GET_SHMEM_CONFIG` (44), which crosvm 18bc84d0
  doesn't speak. Those are left as they are; crosvm's are added beside them, renamed
  `CROSVM_SHMEM_MAP`/`CROSVM_SHMEM_UNMAP` and handled by new `crosvm_shmem_map`/`crosvm_shmem_unmap`
  methods. Only crosvm's always get a reply, as the patch had it.
- `0004-vhost_user-add-protocol-flag-for-shmem.patch`: the protocol bit (`0x8000_0000`) after
  v0.22's new ones.

Nothing in Cloud Hypervisor sends or accepts any of these but the GPU device, which only exists
with `--gpu`.

## cloud-hypervisor/: Cloud Hypervisor v53.0

Spectrum OS's `cloud-hypervisor.patch` (50.0-spectrum0), rebased from v50.0:

- `0001-build-use-local-vhost.patch`: points the workspace at the patched vhost crates;
  `default-features = false` kept, as v53 has it.
- `0002-virtio-devices-add-a-GPU-device.patch`: `--gpu socket=…`, `ch-remote add-gpu`,
  `/vm.add-gpu`, and the vhost-user GPU device with its 8 GiB shared-memory BAR. Rebased onto
  v53's APIs: `GpuConfig` takes the flattened `PciDeviceCommonConfig` (`id`, `pci_segment`,
  `pci_device_id`) and Landlock rules as v53's `FsConfig` does; the device follows v53's
  `VirtioDevice` (`ActivationContext`, `VhostUserCommon` holding `VirtioCommon`, `spawn_worker`,
  `reset`); it is added beside v53's new generic vhost-user device wherever both register; and
  `ActivateError::VhostUserGpuSetup` gets its own message, which had taken `CreateSeccompFilter`'s.
  Beyond the GPU, it changes `VirtioSharedMemoryList::region_list` from a `Vec` to a `BTreeMap`
  keyed by shmid, read only by the PCI transport and set only by the GPU device (virtio-fs's cache
  is always `None` in v53).

Spectrum's backport of upstream 57e766bd (no double activation with 2+ vCPUs, issue #7766) isn't
needed: it is in v52.0 and so in v53.0.

The binary is built as upstream's `cloud-hypervisor-static` is (`--all --release --features
mshv`, musl), with `CH_EXTRA_VERSION=agentbox-<hash of the build's inputs>`: it says `v53.0.0-agentbox-<hash>`.
`test/chv_args_test.sh` runs it and upstream's own v53.0 on AgentBox's VM's command line without
the GPU and checks they say the same.

## crosvm/: crosvm 18bc84d0, the commit Spectrum tested the patchset with

- `0001-gpu-render-node-parameter.patch`: ours, not upstream (crosvm's main has no equivalent). A
  `render-node` key in `device gpu --params`, added as a GPU path for rutabaga, which hands its fd
  to virglrenderer (`get_drm_fd`). Without it virglrenderer renders GL on the first
  `/dev/dri/renderD*` that `readdir` returns: on a machine with an integrated and a discrete GPU,
  the integrated one. crosvm runs with `"surfaceless": false` too, since a surfaceless EGL
  display is Mesa's default device whatever the fd.

## rutabaga_gfx/: the crates.io crate rutabaga_gfx 0.1.75, which crosvm renders with

Fetched at the version and checksum crosvm's Cargo.lock pins, then used from a path, with cargo
offline from there.

- `0001-accept-render-node-gpu-paths.patch`: ours. `is_valid_gpu_path` checked
  `Path::starts_with("/dev/dri/renderD")`, which compares whole components, so no render node was
  ever accepted and `get_drm_fd` always fell back to virglrenderer's own pick. The patch compares
  the path as a string (with a unit test the build runs, `test/render-node-path.sh`), and logs the
  path it takes (`virglrenderer: using GPU path …`), or that there was none. Confirmed on the
  user's RX 6750 XT: GL and Venus both on it.

## Moving to a newer Cloud Hypervisor

1. Clone it and rust-vmm/vhost at the tags it uses (its `Cargo.toml`), apply these series with
   `git am -3`, and fix what conflicts: in v50 → v53, 55 conflict hunks in the GPU commit, nearly all
   of them a new upstream device (the generic vhost-user one) registered where the GPU one is, plus
   the `VirtioDevice` API change, which meant porting `gpu.rs` after `vhost_user/fs.rs`.
2. `cargo clippy`, and `cargo test -p vmm -p virtio-devices -p cloud-hypervisor` (the tests that
   need `/dev/kvm` fail without it, as upstream's do).
3. `git format-patch` into these directories, bump `CHV_VERSION` in `versions.env`, run `build.sh`
   and `test/chv_args_test.sh` here, and `try.sh` on a machine with KVM and a GPU. Merged, the
   Cloud Hypervisor workflow publishes the build and opens the pull request that pins it.
4. Watch upstream for the GPU: if crosvm moves to the standard vhost-user shared-memory messages,
   or Cloud Hypervisor grows them, `vhost/0003` and most of `0002` can go.

LICENSES/ holds the licences Spectrum's patches carry (Apache-2.0, and Google's BSD-3-Clause
for the parts from crosvm).
