// onnxruntime-web's WebGPU ConvTranspose finds the input a filter tap reads
// by dividing in floating point, (corner + w) / stride, and skips the tap when
// the quotient has a fraction. WGSL doesn't promise that division is exact,
// and AMD's Vulkan driver (RADV) does it as a multiply by the reciprocal: 12 / 6
// comes out as 1.9999999, so taps are dropped or read from the wrong input.
// Kokoro's decoder upsamples with a stride of 6, and on those GPUs it said
// nothing but a loud buzz. This patch does the division in integers, which
// every GPU gets right. onnxruntime-web 1.30 still has the bug, in its JSEP
// shaders and its native WebGPU ones alike; once it doesn't, the build fails
// here (kokoroPatch in vite.config.mts), and this can go.

const division = (axis: 'R' | 'C', stride: string) =>
  new RegExp(String.raw`let dy${axis} = \(\$\{(\w+)\}\(dy${axis}Corner\) \+ \$\{\1\}\(w${axis}\)\) / \$\{\1\}\(uniforms\.strides${stride.replace(/[[\].]/g, '\\$&')}\)`, 'g');

const exact = (axis: 'R' | 'C', stride: string, type: string) => {
  const n = `(dy${axis}Corner + i32(w${axis}))`;
  const s = `i32(uniforms.strides${stride})`;
  // Not a multiple of the stride: a value with a fraction, which the shader
  // skips as it always did.
  return `let dy${axis} = select(\${${type}}(0.5), \${${type}}(${n} / ${s}), ${n} % ${s} == 0)`;
};

// patchConvTranspose returns onnxruntime-web's code with the division made
// exact, and the number of places it changed.
export function patchConvTranspose(code: string): { code: string; patched: number } {
  let patched = 0;
  for (const [axis, stride] of [
    ['R', '[0]'],
    ['C', '.y'],
  ] as const) {
    code = code.replace(division(axis, stride), (_, type: string) => {
      patched++;
      return exact(axis, stride, type);
    });
  }
  return { code, patched };
}
