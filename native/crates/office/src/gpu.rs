//! The Office's renderer: wgpu drawing offscreen into a texture that is read
//! back as BGRA, the bytes GPUI paints as an image. Offscreen rather than a
//! child surface so GPUI's own elements (name pills, the chat) draw over the
//! room, and so it works the same on X11, Wayland, macOS and Windows.

use anyhow::{Context as _, Result, anyhow};
use bytemuck::{Pod, Zeroable};
use glam::{Mat4, Vec3};

#[repr(C)]
#[derive(Clone, Copy, Debug, Default, Pod, Zeroable)]
pub struct Vertex {
    pub pos: [f32; 3],
    pub normal: [f32; 3],
    pub uv: [f32; 2],
    pub color: [f32; 4],
}

/// One mesh to upload: its triangles and, optionally, the texture (an index
/// from [`Gpu::add_texture`]) its colours are multiplied by.
#[derive(Clone, Debug, Default)]
pub struct MeshData {
    pub vertices: Vec<Vertex>,
    pub indices: Vec<u32>,
    pub texture: Option<usize>,
    /// Seen through (glass): drawn after everything else, not hiding what's behind.
    pub transparent: bool,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Hash)]
pub struct MeshId(pub usize);

/// One copy of a mesh: where it is and a colour it's tinted with.
#[repr(C)]
#[derive(Clone, Copy, Debug, Pod, Zeroable)]
pub struct Instance {
    pub model: [[f32; 4]; 4],
    pub tint: [f32; 4],
}

impl Instance {
    pub fn new(model: Mat4, tint: [f32; 4]) -> Self {
        Self {
            model: model.to_cols_array_2d(),
            tint,
        }
    }
}

#[derive(Clone, Copy, Debug)]
pub struct Camera {
    pub view_proj: Mat4,
    pub eye: Vec3,
}

#[repr(C)]
#[derive(Clone, Copy, Pod, Zeroable)]
struct Globals {
    view_proj: [[f32; 4]; 4],
    sun: [f32; 4],
    sky: [f32; 4],
    ground: [f32; 4],
    sun_color: [f32; 4],
    eye: [f32; 4],
    fog: [f32; 4],
}

struct GpuMesh {
    vertices: wgpu::Buffer,
    indices: wgpu::Buffer,
    count: u32,
    texture: usize,
    transparent: bool,
}

struct GpuTexture {
    texture: wgpu::Texture,
    w: u32,
    h: u32,
    palette: bool,
    bind: wgpu::BindGroup,
}

struct Target {
    w: u32,
    h: u32,
    color: wgpu::Texture,
    /// With MSAA, what's drawn into before it's resolved into `color`.
    msaa: Option<wgpu::TextureView>,
    depth: wgpu::TextureView,
    readback: wgpu::Buffer,
    row: u32,
}

/// A frame read back: BGRA8, sRGB, `width * 4` bytes a row.
pub struct Frame {
    pub width: u32,
    pub height: u32,
    pub bgra: Vec<u8>,
}

pub struct Gpu {
    device: wgpu::Device,
    queue: wgpu::Queue,
    pipeline: wgpu::RenderPipeline,
    glass: wgpu::RenderPipeline,
    globals: wgpu::Buffer,
    globals_bind: wgpu::BindGroup,
    texture_layout: wgpu::BindGroupLayout,
    /// Pictures (signs, floors, screens): trilinear with mips, anisotropic
    /// where the adapter has it, so a floor seen far off and at a slant
    /// doesn't shimmer as you move.
    sampler: wgpu::Sampler,
    /// Palettes (Kenney's colormap: flat swatches a model's UVs pick from):
    /// nearest, no mips, which would blend a swatch with its neighbours.
    palette_sampler: wgpu::Sampler,
    textures: Vec<GpuTexture>,
    samples: u32,
    meshes: Vec<GpuMesh>,
    instances: wgpu::Buffer,
    instance_cap: u64,
    target: Option<Target>,
    pub adapter_name: String,
    /// Drawn on the CPU (lavapipe, llvmpipe, WARP): slow, and the Office says so.
    pub software: bool,
}

const COLOR: wgpu::TextureFormat = wgpu::TextureFormat::Bgra8UnormSrgb;
const DEPTH: wgpu::TextureFormat = wgpu::TextureFormat::Depth32Float;

/// Reversed depth: the camera's projection (near 0.1, far 900) maps near to
/// 1 and far to 0. A float depth buffer has most of its precision near 0,
/// which the standard mapping spends on the first metre; reversed, it's
/// spread over the whole view, so floors, rugs and decals a millimetre apart
/// stay apart even from the campus view instead of fighting (flickering) as
/// the camera moves.
const REVERSE_Z: Mat4 = Mat4::from_cols_array(&[
    1., 0., 0., 0., //
    0., 1., 0., 0., //
    0., 0., -1., 0., //
    0., 0., 1., 1.,
]);

impl Gpu {
    pub fn new() -> Result<Self> {
        pollster::block_on(Self::new_async())
    }

    async fn new_async() -> Result<Self> {
        // Offscreen: no window's display, and WGPU_BACKEND and the like honoured.
        let mut desc = wgpu::InstanceDescriptor::new_without_display_handle_from_env();
        desc.backends &= wgpu::Backends::PRIMARY;
        let instance = wgpu::Instance::new(desc);
        let adapter = instance
            .request_adapter(&wgpu::RequestAdapterOptions {
                power_preference: wgpu::PowerPreference::LowPower,
                compatible_surface: None,
                force_fallback_adapter: false,
                apply_limit_buckets: false,
            })
            .await
            .context("no GPU adapter (Vulkan, Metal or DX12)")?;
        let info = adapter.get_info();
        let adapter_name = info.name.clone();
        let software = info.device_type == wgpu::DeviceType::Cpu;
        // Edges crawl as the camera moves without multisampling; a GPU does
        // 4× for little, the CPU (lavapipe) pays for every sample, so it
        // goes without unless AGENTBOX_OFFICE_MSAA says otherwise.
        let samples = match std::env::var("AGENTBOX_OFFICE_MSAA").ok().as_deref() {
            Some("1") => 1,
            Some("4") => 4,
            _ if software => 1,
            _ => 4,
        };
        let aniso = adapter
            .get_downlevel_capabilities()
            .flags
            .contains(wgpu::DownlevelFlags::ANISOTROPIC_FILTERING);
        let (device, queue) = adapter
            .request_device(&wgpu::DeviceDescriptor {
                label: Some("office"),
                required_limits: wgpu::Limits::downlevel_defaults()
                    .using_resolution(adapter.limits()),
                ..Default::default()
            })
            .await
            .context("open the GPU device")?;

        let shader = device.create_shader_module(wgpu::ShaderModuleDescriptor {
            label: Some("office"),
            source: wgpu::ShaderSource::Wgsl(include_str!("office.wgsl").into()),
        });
        let globals_layout = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
            label: Some("globals"),
            entries: &[wgpu::BindGroupLayoutEntry {
                binding: 0,
                visibility: wgpu::ShaderStages::VERTEX_FRAGMENT,
                ty: wgpu::BindingType::Buffer {
                    ty: wgpu::BufferBindingType::Uniform,
                    has_dynamic_offset: false,
                    min_binding_size: None,
                },
                count: None,
            }],
        });
        let texture_layout = device.create_bind_group_layout(&wgpu::BindGroupLayoutDescriptor {
            label: Some("texture"),
            entries: &[
                wgpu::BindGroupLayoutEntry {
                    binding: 0,
                    visibility: wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Texture {
                        sample_type: wgpu::TextureSampleType::Float { filterable: true },
                        view_dimension: wgpu::TextureViewDimension::D2,
                        multisampled: false,
                    },
                    count: None,
                },
                wgpu::BindGroupLayoutEntry {
                    binding: 1,
                    visibility: wgpu::ShaderStages::FRAGMENT,
                    ty: wgpu::BindingType::Sampler(wgpu::SamplerBindingType::Filtering),
                    count: None,
                },
            ],
        });
        let layout = device.create_pipeline_layout(&wgpu::PipelineLayoutDescriptor {
            label: Some("office"),
            bind_group_layouts: &[Some(&globals_layout), Some(&texture_layout)],
            ..Default::default()
        });
        let attrs = wgpu::vertex_attr_array![0 => Float32x3, 1 => Float32x3, 2 => Float32x2, 3 => Float32x4];
        let inst_attrs = wgpu::vertex_attr_array![4 => Float32x4, 5 => Float32x4, 6 => Float32x4, 7 => Float32x4, 8 => Float32x4];
        let make = |glass: bool| {
            device.create_render_pipeline(&wgpu::RenderPipelineDescriptor {
                label: Some("office"),
                layout: Some(&layout),
                vertex: wgpu::VertexState {
                    module: &shader,
                    entry_point: Some("vs"),
                    compilation_options: Default::default(),
                    buffers: &[
                        Some(wgpu::VertexBufferLayout {
                            array_stride: std::mem::size_of::<Vertex>() as u64,
                            step_mode: wgpu::VertexStepMode::Vertex,
                            attributes: &attrs,
                        }),
                        Some(wgpu::VertexBufferLayout {
                            array_stride: std::mem::size_of::<Instance>() as u64,
                            step_mode: wgpu::VertexStepMode::Instance,
                            attributes: &inst_attrs,
                        }),
                    ],
                },
                fragment: Some(wgpu::FragmentState {
                    module: &shader,
                    entry_point: Some("fs"),
                    compilation_options: Default::default(),
                    targets: &[Some(wgpu::ColorTargetState {
                        format: COLOR,
                        blend: Some(wgpu::BlendState::ALPHA_BLENDING),
                        write_mask: wgpu::ColorWrites::ALL,
                    })],
                }),
                primitive: wgpu::PrimitiveState {
                    cull_mode: None,
                    ..Default::default()
                },
                depth_stencil: Some(wgpu::DepthStencilState {
                    format: DEPTH,
                    depth_write_enabled: Some(!glass),
                    depth_compare: Some(wgpu::CompareFunction::Greater),
                    stencil: Default::default(),
                    bias: Default::default(),
                }),
                multisample: wgpu::MultisampleState {
                    count: samples,
                    ..Default::default()
                },
                multiview_mask: None,
                cache: None,
            })
        };
        let pipeline = make(false);
        let glass = make(true);
        let globals = device.create_buffer(&wgpu::BufferDescriptor {
            label: Some("globals"),
            size: std::mem::size_of::<Globals>() as u64,
            usage: wgpu::BufferUsages::UNIFORM | wgpu::BufferUsages::COPY_DST,
            mapped_at_creation: false,
        });
        let globals_bind = device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: Some("globals"),
            layout: &globals_layout,
            entries: &[wgpu::BindGroupEntry {
                binding: 0,
                resource: globals.as_entire_binding(),
            }],
        });
        let sampler = device.create_sampler(&wgpu::SamplerDescriptor {
            mag_filter: wgpu::FilterMode::Linear,
            min_filter: wgpu::FilterMode::Linear,
            mipmap_filter: wgpu::MipmapFilterMode::Linear,
            anisotropy_clamp: if aniso { 8 } else { 1 },
            address_mode_u: wgpu::AddressMode::Repeat,
            address_mode_v: wgpu::AddressMode::Repeat,
            ..Default::default()
        });
        let palette_sampler = device.create_sampler(&wgpu::SamplerDescriptor {
            mag_filter: wgpu::FilterMode::Nearest,
            min_filter: wgpu::FilterMode::Nearest,
            address_mode_u: wgpu::AddressMode::Repeat,
            address_mode_v: wgpu::AddressMode::Repeat,
            ..Default::default()
        });
        let instance_cap = 1024;
        let instances = device.create_buffer(&wgpu::BufferDescriptor {
            label: Some("instances"),
            size: instance_cap * std::mem::size_of::<Instance>() as u64,
            usage: wgpu::BufferUsages::VERTEX | wgpu::BufferUsages::COPY_DST,
            mapped_at_creation: false,
        });
        let mut gpu = Self {
            device,
            queue,
            pipeline,
            glass,
            globals,
            globals_bind,
            texture_layout,
            sampler,
            palette_sampler,
            textures: Vec::new(),
            samples,
            meshes: Vec::new(),
            instances,
            instance_cap,
            target: None,
            adapter_name,
            software,
        };
        // Texture 0: white, for meshes coloured by their vertices alone.
        gpu.add_texture(1, 1, &[255, 255, 255, 255]);
        Ok(gpu)
    }

    /// add_texture uploads an RGBA8 (sRGB) image and returns its index.
    pub fn add_texture(&mut self, w: u32, h: u32, rgba: &[u8]) -> usize {
        let t = self.make_texture(w, h, rgba);
        self.textures.push(t);
        self.textures.len() - 1
    }

    fn make_texture(&self, w: u32, h: u32, rgba: &[u8]) -> GpuTexture {
        let palette = is_palette(rgba);
        let levels = if palette { 1 } else { mip_levels(w, h) };
        let texture = self.device.create_texture(&wgpu::TextureDescriptor {
            label: None,
            size: wgpu::Extent3d {
                width: w,
                height: h,
                depth_or_array_layers: 1,
            },
            mip_level_count: levels,
            sample_count: 1,
            dimension: wgpu::TextureDimension::D2,
            format: wgpu::TextureFormat::Rgba8UnormSrgb,
            usage: wgpu::TextureUsages::TEXTURE_BINDING | wgpu::TextureUsages::COPY_DST,
            view_formats: &[],
        });
        let view = texture.create_view(&Default::default());
        let bind = self.device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: None,
            layout: &self.texture_layout,
            entries: &[
                wgpu::BindGroupEntry {
                    binding: 0,
                    resource: wgpu::BindingResource::TextureView(&view),
                },
                wgpu::BindGroupEntry {
                    binding: 1,
                    resource: wgpu::BindingResource::Sampler(if palette {
                        &self.palette_sampler
                    } else {
                        &self.sampler
                    }),
                },
            ],
        });
        let t = GpuTexture {
            texture,
            w,
            h,
            palette,
            bind,
        };
        self.write_texture(&t, rgba);
        t
    }

    /// write_texture uploads an image and its mips into `t`.
    fn write_texture(&self, t: &GpuTexture, rgba: &[u8]) {
        let levels = t.texture.mip_level_count();
        let mut level = std::borrow::Cow::Borrowed(rgba);
        let (mut w, mut h) = (t.w, t.h);
        for mip in 0..levels {
            if mip > 0 {
                let (next, nw, nh) = downsample(&level, w, h);
                level = std::borrow::Cow::Owned(next);
                (w, h) = (nw, nh);
            }
            self.queue.write_texture(
                wgpu::TexelCopyTextureInfo {
                    texture: &t.texture,
                    mip_level: mip,
                    origin: wgpu::Origin3d::ZERO,
                    aspect: wgpu::TextureAspect::All,
                },
                &level,
                wgpu::TexelCopyBufferLayout {
                    offset: 0,
                    bytes_per_row: Some(4 * w),
                    rows_per_image: None,
                },
                wgpu::Extent3d {
                    width: w,
                    height: h,
                    depth_or_array_layers: 1,
                },
            );
        }
    }

    /// replace_texture redraws a texture (a monitor's screen, the board):
    /// in place when it's the same size and kind, else as a new one under the
    /// same index. In place, the queue orders the write after the frames
    /// already drawn with the old picture, and nothing is allocated.
    pub fn replace_texture(&mut self, index: usize, w: u32, h: u32, rgba: &[u8]) {
        let t = &self.textures[index];
        if t.w == w && t.h == h && t.palette == is_palette(rgba) {
            self.write_texture(t, rgba);
            return;
        }
        self.textures[index] = self.make_texture(w, h, rgba);
    }

    /// clear_meshes drops every mesh (a new campus is built).
    pub fn clear_meshes(&mut self) {
        self.meshes.clear();
    }

    pub fn add_mesh(&mut self, m: &MeshData) -> MeshId {
        use wgpu::util::DeviceExt as _;
        let vertices = self
            .device
            .create_buffer_init(&wgpu::util::BufferInitDescriptor {
                label: None,
                contents: bytemuck::cast_slice(&m.vertices),
                usage: wgpu::BufferUsages::VERTEX | wgpu::BufferUsages::COPY_DST,
            });
        let indices = self
            .device
            .create_buffer_init(&wgpu::util::BufferInitDescriptor {
                label: None,
                contents: bytemuck::cast_slice(&m.indices),
                usage: wgpu::BufferUsages::INDEX,
            });
        self.meshes.push(GpuMesh {
            vertices,
            indices,
            count: m.indices.len() as u32,
            texture: m.texture.unwrap_or(0),
            transparent: m.transparent,
        });
        MeshId(self.meshes.len() - 1)
    }

    /// Replaces a mesh's triangles in place (a skinned figure, posed on the CPU).
    pub fn update_mesh(&mut self, id: MeshId, vertices: &[Vertex]) {
        self.queue.write_buffer(
            &self.meshes[id.0].vertices,
            0,
            bytemuck::cast_slice(vertices),
        );
    }

    fn target(&mut self, w: u32, h: u32) -> &Target {
        if self.target.as_ref().is_none_or(|t| t.w != w || t.h != h) {
            let size = wgpu::Extent3d {
                width: w,
                height: h,
                depth_or_array_layers: 1,
            };
            let color = self.device.create_texture(&wgpu::TextureDescriptor {
                label: Some("office color"),
                size,
                mip_level_count: 1,
                sample_count: 1,
                dimension: wgpu::TextureDimension::D2,
                format: COLOR,
                usage: wgpu::TextureUsages::RENDER_ATTACHMENT | wgpu::TextureUsages::COPY_SRC,
                view_formats: &[],
            });
            let msaa = (self.samples > 1).then(|| {
                self.device
                    .create_texture(&wgpu::TextureDescriptor {
                        label: Some("office msaa"),
                        size,
                        mip_level_count: 1,
                        sample_count: self.samples,
                        dimension: wgpu::TextureDimension::D2,
                        format: COLOR,
                        usage: wgpu::TextureUsages::RENDER_ATTACHMENT,
                        view_formats: &[],
                    })
                    .create_view(&Default::default())
            });
            let depth = self
                .device
                .create_texture(&wgpu::TextureDescriptor {
                    label: Some("office depth"),
                    size,
                    mip_level_count: 1,
                    sample_count: self.samples,
                    dimension: wgpu::TextureDimension::D2,
                    format: DEPTH,
                    usage: wgpu::TextureUsages::RENDER_ATTACHMENT,
                    view_formats: &[],
                })
                .create_view(&Default::default());
            let row = (w * 4).div_ceil(wgpu::COPY_BYTES_PER_ROW_ALIGNMENT)
                * wgpu::COPY_BYTES_PER_ROW_ALIGNMENT;
            let readback = self.device.create_buffer(&wgpu::BufferDescriptor {
                label: Some("office readback"),
                size: (row * h) as u64,
                usage: wgpu::BufferUsages::COPY_DST | wgpu::BufferUsages::MAP_READ,
                mapped_at_creation: false,
            });
            self.target = Some(Target {
                w,
                h,
                color,
                msaa,
                depth,
                readback,
                row,
            });
        }
        self.target.as_ref().unwrap()
    }

    /// render draws `draws` (each mesh with its copies) from `camera` at
    /// w×h and reads the picture back.
    pub fn render(
        &mut self,
        w: u32,
        h: u32,
        camera: &Camera,
        clear: [f64; 3],
        draws: &[(MeshId, Vec<Instance>)],
    ) -> Result<Frame> {
        let w = w.max(1);
        let h = h.max(1);
        let total: u64 = draws.iter().map(|(_, i)| i.len() as u64).sum();
        if total > self.instance_cap {
            self.instance_cap = total.next_power_of_two();
            self.instances = self.device.create_buffer(&wgpu::BufferDescriptor {
                label: Some("instances"),
                size: self.instance_cap * std::mem::size_of::<Instance>() as u64,
                usage: wgpu::BufferUsages::VERTEX | wgpu::BufferUsages::COPY_DST,
                mapped_at_creation: false,
            });
        }
        let mut flat: Vec<Instance> = Vec::with_capacity(total as usize);
        for (_, i) in draws {
            flat.extend_from_slice(i);
        }
        if !flat.is_empty() {
            self.queue
                .write_buffer(&self.instances, 0, bytemuck::cast_slice(&flat));
        }
        let sun = Vec3::new(-0.45, 1.0, 0.6).normalize();
        let globals = Globals {
            view_proj: (REVERSE_Z * camera.view_proj).to_cols_array_2d(),
            sun: [sun.x, sun.y, sun.z, 0.],
            sky: [0.78, 0.80, 0.86, 1.],
            ground: [0.42, 0.38, 0.36, 1.],
            sun_color: [0.85, 0.80, 0.72, 1.],
            eye: [camera.eye.x, camera.eye.y, camera.eye.z, 1.],
            fog: [clear[0] as f32, clear[1] as f32, clear[2] as f32, 0.],
        };
        self.queue
            .write_buffer(&self.globals, 0, bytemuck::bytes_of(&globals));
        self.target(w, h);
        let t = self.target.as_ref().unwrap();
        let view = t.color.create_view(&Default::default());
        let mut enc = self.device.create_command_encoder(&Default::default());
        {
            let mut pass = enc.begin_render_pass(&wgpu::RenderPassDescriptor {
                label: Some("office"),
                color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                    view: t.msaa.as_ref().unwrap_or(&view),
                    depth_slice: None,
                    resolve_target: t.msaa.is_some().then_some(&view),
                    ops: wgpu::Operations {
                        load: wgpu::LoadOp::Clear(wgpu::Color {
                            r: clear[0],
                            g: clear[1],
                            b: clear[2],
                            a: 1.,
                        }),
                        // Multisampled, only the resolved picture is kept.
                        store: if t.msaa.is_some() {
                            wgpu::StoreOp::Discard
                        } else {
                            wgpu::StoreOp::Store
                        },
                    },
                })],
                depth_stencil_attachment: Some(wgpu::RenderPassDepthStencilAttachment {
                    view: &t.depth,
                    depth_ops: Some(wgpu::Operations {
                        load: wgpu::LoadOp::Clear(0.),
                        store: wgpu::StoreOp::Discard,
                    }),
                    stencil_ops: None,
                }),
                ..Default::default()
            });
            pass.set_bind_group(0, &self.globals_bind, &[]);
            pass.set_vertex_buffer(1, self.instances.slice(..));
            // Solid things first, then glass over them.
            for glass in [false, true] {
                pass.set_pipeline(if glass { &self.glass } else { &self.pipeline });
                let mut first = 0u32;
                for (id, inst) in draws {
                    let n = inst.len() as u32;
                    let m = &self.meshes[id.0];
                    if n > 0 && m.transparent == glass {
                        pass.set_bind_group(1, &self.textures[m.texture].bind, &[]);
                        pass.set_vertex_buffer(0, m.vertices.slice(..));
                        pass.set_index_buffer(m.indices.slice(..), wgpu::IndexFormat::Uint32);
                        pass.draw_indexed(0..m.count, 0, first..first + n);
                    }
                    first += n;
                }
            }
        }
        enc.copy_texture_to_buffer(
            t.color.as_image_copy(),
            wgpu::TexelCopyBufferInfo {
                buffer: &t.readback,
                layout: wgpu::TexelCopyBufferLayout {
                    offset: 0,
                    bytes_per_row: Some(t.row),
                    rows_per_image: None,
                },
            },
            wgpu::Extent3d {
                width: w,
                height: h,
                depth_or_array_layers: 1,
            },
        );
        self.queue.submit([enc.finish()]);
        let slice = t.readback.slice(..);
        let (tx, rx) = std::sync::mpsc::channel();
        slice.map_async(wgpu::MapMode::Read, move |r| {
            let _ = tx.send(r);
        });
        self.device
            .poll(wgpu::PollType::wait_indefinitely())
            .map_err(|e| anyhow!("{e}"))?;
        rx.recv()?.map_err(|e| anyhow!("{e}"))?;
        let mut bgra = Vec::with_capacity((w * h * 4) as usize);
        {
            let data = slice.get_mapped_range().map_err(|e| anyhow!("{e:?}"))?;
            if t.row == w * 4 {
                // Rows already packed (widths a multiple of 64): one copy.
                bgra.extend_from_slice(&data[..(w * h * 4) as usize]);
            } else {
                for y in 0..h {
                    let at = (y * t.row) as usize;
                    bgra.extend_from_slice(&data[at..at + (w * 4) as usize]);
                }
            }
        }
        t.readback.unmap();
        Ok(Frame {
            width: w,
            height: h,
            bgra,
        })
    }
}

/// is_palette is whether an image is a palette of flat swatches (Kenney's
/// colormap) rather than a picture: few colours, none blended. Pictures drawn
/// with antialiasing, noise or gradients have thousands.
fn is_palette(rgba: &[u8]) -> bool {
    let mut seen = std::collections::HashSet::new();
    for px in rgba.as_chunks::<4>().0 {
        seen.insert(u32::from_le_bytes(*px));
        if seen.len() > 256 {
            return false;
        }
    }
    true
}

fn mip_levels(w: u32, h: u32) -> u32 {
    32 - w.max(h).max(1).leading_zeros()
}

fn srgb_to_linear(c: u8) -> f32 {
    let c = c as f32 / 255.;
    if c <= 0.04045 {
        c / 12.92
    } else {
        ((c + 0.055) / 1.055).powf(2.4)
    }
}

fn linear_to_srgb(c: f32) -> u8 {
    let c = c.clamp(0., 1.);
    let s = if c <= 0.003_130_8 {
        c * 12.92
    } else {
        1.055 * c.powf(1. / 2.4) - 0.055
    };
    (s * 255. + 0.5) as u8
}

/// downsample halves an sRGB image (a 2×2 box, averaged in linear light so
/// a mip isn't darker than what it stands for; odd edges clamp).
fn downsample(rgba: &[u8], w: u32, h: u32) -> (Vec<u8>, u32, u32) {
    let lut: Vec<f32> = (0..=255u8).map(srgb_to_linear).collect();
    let (nw, nh) = ((w / 2).max(1), (h / 2).max(1));
    let mut out = vec![0u8; (nw * nh * 4) as usize];
    for y in 0..nh {
        for x in 0..nw {
            let mut acc = [0f32; 4];
            for (dx, dy) in [(0, 0), (1, 0), (0, 1), (1, 1)] {
                let sx = (x * 2 + dx).min(w - 1);
                let sy = (y * 2 + dy).min(h - 1);
                let i = ((sy * w + sx) * 4) as usize;
                for c in 0..3 {
                    acc[c] += lut[rgba[i + c] as usize];
                }
                acc[3] += rgba[i + 3] as f32;
            }
            let o = ((y * nw + x) * 4) as usize;
            for c in 0..3 {
                out[o + c] = linear_to_srgb(acc[c] / 4.);
            }
            out[o + 3] = (acc[3] / 4. + 0.5) as u8;
        }
    }
    (out, nw, nh)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn reversed_depth_puts_near_at_one_and_far_at_zero() {
        let proj = Mat4::perspective_rh(1., 1., 0.1, 900.);
        let depth = |z: f32| {
            let c = REVERSE_Z * proj * glam::Vec4::new(0., 0., -z, 1.);
            c.z / c.w
        };
        assert!((depth(0.1) - 1.).abs() < 1e-5);
        assert!(depth(900.).abs() < 1e-5);
        assert!(depth(10.) > depth(10.001), "nearer is greater");
    }

    #[test]
    fn floors_a_millimetre_apart_keep_apart_from_the_campus_view() {
        // A floor at 60 m with a rug 1 mm over it: different depths in f32.
        let proj = REVERSE_Z * Mat4::perspective_rh(1., 1., 0.1, 900.);
        let d = |z: f32| {
            let c = proj * glam::Vec4::new(0., 0., -z, 1.);
            c.z / c.w
        };
        assert!(d(60.) > d(60.001));
    }

    #[test]
    fn palettes_and_pictures() {
        let flat: Vec<u8> = (0..64 * 64)
            .flat_map(|i| [(i / 512 * 30) as u8, 0, 0, 255])
            .collect();
        assert!(is_palette(&flat));
        let noise: Vec<u8> = (0..64 * 64u32)
            .flat_map(|i| [(i * 7) as u8, (i * 13) as u8, (i / 3) as u8, 255])
            .collect();
        assert!(!is_palette(&noise));
        let colormap = crate::glb::decode_png(crate::assets::COLORMAP).unwrap();
        assert!(
            is_palette(&colormap.rgba),
            "Kenney's colormap keeps nearest, no mips"
        );
    }

    #[test]
    fn mips_average_in_linear_light() {
        assert_eq!(mip_levels(512, 256), 10);
        assert_eq!(mip_levels(1, 1), 1);
        // Black and white checks average to sRGB 188, not 128.
        let img = [
            0, 0, 0, 255, 255, 255, 255, 255, 255, 255, 255, 255, 0, 0, 0, 255,
        ];
        let (half, w, h) = downsample(&img, 2, 2);
        assert_eq!((w, h), (1, 1));
        assert_eq!(half[0], 188);
    }
}
