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
        Self { model: model.to_cols_array_2d(), tint }
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
}

struct Target {
    w: u32,
    h: u32,
    color: wgpu::Texture,
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
    globals: wgpu::Buffer,
    globals_bind: wgpu::BindGroup,
    texture_layout: wgpu::BindGroupLayout,
    sampler: wgpu::Sampler,
    textures: Vec<wgpu::BindGroup>,
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
        let (device, queue) = adapter
            .request_device(&wgpu::DeviceDescriptor {
                label: Some("office"),
                required_limits: wgpu::Limits::downlevel_defaults().using_resolution(adapter.limits()),
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
        let pipeline = device.create_render_pipeline(&wgpu::RenderPipelineDescriptor {
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
                depth_write_enabled: Some(true),
                depth_compare: Some(wgpu::CompareFunction::Less),
                stencil: Default::default(),
                bias: Default::default(),
            }),
            multisample: Default::default(),
            multiview_mask: None,
            cache: None,
        });
        let globals = device.create_buffer(&wgpu::BufferDescriptor {
            label: Some("globals"),
            size: std::mem::size_of::<Globals>() as u64,
            usage: wgpu::BufferUsages::UNIFORM | wgpu::BufferUsages::COPY_DST,
            mapped_at_creation: false,
        });
        let globals_bind = device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: Some("globals"),
            layout: &globals_layout,
            entries: &[wgpu::BindGroupEntry { binding: 0, resource: globals.as_entire_binding() }],
        });
        let sampler = device.create_sampler(&wgpu::SamplerDescriptor {
            mag_filter: wgpu::FilterMode::Nearest,
            min_filter: wgpu::FilterMode::Linear,
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
            globals,
            globals_bind,
            texture_layout,
            sampler,
            textures: Vec::new(),
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
        let size = wgpu::Extent3d { width: w, height: h, depth_or_array_layers: 1 };
        let tex = self.device.create_texture(&wgpu::TextureDescriptor {
            label: None,
            size,
            mip_level_count: 1,
            sample_count: 1,
            dimension: wgpu::TextureDimension::D2,
            format: wgpu::TextureFormat::Rgba8UnormSrgb,
            usage: wgpu::TextureUsages::TEXTURE_BINDING | wgpu::TextureUsages::COPY_DST,
            view_formats: &[],
        });
        self.queue.write_texture(
            tex.as_image_copy(),
            rgba,
            wgpu::TexelCopyBufferLayout { offset: 0, bytes_per_row: Some(4 * w), rows_per_image: None },
            size,
        );
        let view = tex.create_view(&Default::default());
        let bind = self.device.create_bind_group(&wgpu::BindGroupDescriptor {
            label: None,
            layout: &self.texture_layout,
            entries: &[
                wgpu::BindGroupEntry { binding: 0, resource: wgpu::BindingResource::TextureView(&view) },
                wgpu::BindGroupEntry { binding: 1, resource: wgpu::BindingResource::Sampler(&self.sampler) },
            ],
        });
        self.textures.push(bind);
        self.textures.len() - 1
    }

    pub fn add_mesh(&mut self, m: &MeshData) -> MeshId {
        use wgpu::util::DeviceExt as _;
        let vertices = self.device.create_buffer_init(&wgpu::util::BufferInitDescriptor {
            label: None,
            contents: bytemuck::cast_slice(&m.vertices),
            usage: wgpu::BufferUsages::VERTEX,
        });
        let indices = self.device.create_buffer_init(&wgpu::util::BufferInitDescriptor {
            label: None,
            contents: bytemuck::cast_slice(&m.indices),
            usage: wgpu::BufferUsages::INDEX,
        });
        self.meshes.push(GpuMesh { vertices, indices, count: m.indices.len() as u32, texture: m.texture.unwrap_or(0) });
        MeshId(self.meshes.len() - 1)
    }

    /// Replaces a mesh's triangles in place (a skinned figure, posed on the CPU).
    pub fn update_mesh(&mut self, id: MeshId, vertices: &[Vertex]) {
        self.queue.write_buffer(&self.meshes[id.0].vertices, 0, bytemuck::cast_slice(vertices));
    }

    fn target(&mut self, w: u32, h: u32) -> &Target {
        if self.target.as_ref().is_none_or(|t| t.w != w || t.h != h) {
            let size = wgpu::Extent3d { width: w, height: h, depth_or_array_layers: 1 };
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
            let depth = self
                .device
                .create_texture(&wgpu::TextureDescriptor {
                    label: Some("office depth"),
                    size,
                    mip_level_count: 1,
                    sample_count: 1,
                    dimension: wgpu::TextureDimension::D2,
                    format: DEPTH,
                    usage: wgpu::TextureUsages::RENDER_ATTACHMENT,
                    view_formats: &[],
                })
                .create_view(&Default::default());
            let row = (w * 4).div_ceil(wgpu::COPY_BYTES_PER_ROW_ALIGNMENT) * wgpu::COPY_BYTES_PER_ROW_ALIGNMENT;
            let readback = self.device.create_buffer(&wgpu::BufferDescriptor {
                label: Some("office readback"),
                size: (row * h) as u64,
                usage: wgpu::BufferUsages::COPY_DST | wgpu::BufferUsages::MAP_READ,
                mapped_at_creation: false,
            });
            self.target = Some(Target { w, h, color, depth, readback, row });
        }
        self.target.as_ref().unwrap()
    }

    /// render draws `draws` (each mesh with its copies) from `camera` at
    /// w×h and reads the picture back.
    pub fn render(&mut self, w: u32, h: u32, camera: &Camera, clear: [f64; 3], draws: &[(MeshId, Vec<Instance>)]) -> Result<Frame> {
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
            self.queue.write_buffer(&self.instances, 0, bytemuck::cast_slice(&flat));
        }
        let sun = Vec3::new(-0.45, 1.0, 0.6).normalize();
        let globals = Globals {
            view_proj: camera.view_proj.to_cols_array_2d(),
            sun: [sun.x, sun.y, sun.z, 0.],
            sky: [0.78, 0.80, 0.86, 1.],
            ground: [0.42, 0.38, 0.36, 1.],
            sun_color: [0.85, 0.80, 0.72, 1.],
            eye: [camera.eye.x, camera.eye.y, camera.eye.z, 1.],
            fog: [clear[0] as f32, clear[1] as f32, clear[2] as f32, 0.],
        };
        self.queue.write_buffer(&self.globals, 0, bytemuck::bytes_of(&globals));
        self.target(w, h);
        let t = self.target.as_ref().unwrap();
        let view = t.color.create_view(&Default::default());
        let mut enc = self.device.create_command_encoder(&Default::default());
        {
            let mut pass = enc.begin_render_pass(&wgpu::RenderPassDescriptor {
                label: Some("office"),
                color_attachments: &[Some(wgpu::RenderPassColorAttachment {
                    view: &view,
                    depth_slice: None,
                    resolve_target: None,
                    ops: wgpu::Operations {
                        load: wgpu::LoadOp::Clear(wgpu::Color { r: clear[0], g: clear[1], b: clear[2], a: 1. }),
                        store: wgpu::StoreOp::Store,
                    },
                })],
                depth_stencil_attachment: Some(wgpu::RenderPassDepthStencilAttachment {
                    view: &t.depth,
                    depth_ops: Some(wgpu::Operations { load: wgpu::LoadOp::Clear(1.), store: wgpu::StoreOp::Discard }),
                    stencil_ops: None,
                }),
                ..Default::default()
            });
            pass.set_pipeline(&self.pipeline);
            pass.set_bind_group(0, &self.globals_bind, &[]);
            pass.set_vertex_buffer(1, self.instances.slice(..));
            let mut first = 0u32;
            for (id, inst) in draws {
                let n = inst.len() as u32;
                if n == 0 {
                    continue;
                }
                let m = &self.meshes[id.0];
                pass.set_bind_group(1, &self.textures[m.texture], &[]);
                pass.set_vertex_buffer(0, m.vertices.slice(..));
                pass.set_index_buffer(m.indices.slice(..), wgpu::IndexFormat::Uint32);
                pass.draw_indexed(0..m.count, 0, first..first + n);
                first += n;
            }
        }
        enc.copy_texture_to_buffer(
            t.color.as_image_copy(),
            wgpu::TexelCopyBufferInfo {
                buffer: &t.readback,
                layout: wgpu::TexelCopyBufferLayout { offset: 0, bytes_per_row: Some(t.row), rows_per_image: None },
            },
            wgpu::Extent3d { width: w, height: h, depth_or_array_layers: 1 },
        );
        self.queue.submit([enc.finish()]);
        let slice = t.readback.slice(..);
        let (tx, rx) = std::sync::mpsc::channel();
        slice.map_async(wgpu::MapMode::Read, move |r| {
            let _ = tx.send(r);
        });
        self.device.poll(wgpu::PollType::wait_indefinitely()).map_err(|e| anyhow!("{e}"))?;
        rx.recv()?.map_err(|e| anyhow!("{e}"))?;
        let mut bgra = Vec::with_capacity((w * h * 4) as usize);
        {
            let data = slice.get_mapped_range().map_err(|e| anyhow!("{e:?}"))?;
            for y in 0..h {
                let at = (y * t.row) as usize;
                bgra.extend_from_slice(&data[at..at + (w * 4) as usize]);
            }
        }
        t.readback.unmap();
        Ok(Frame { width: w, height: h, bgra })
    }
}
