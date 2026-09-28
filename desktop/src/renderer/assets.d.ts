// A file Vite copies into the build as it is, imported for its URL: the
// voice's onnxruntime WebAssembly (lib/voice/kokoro.worker.ts).
declare module '*?url' {
  const url: string;
  export default url;
}
