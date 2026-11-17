// Schemes the OS may open for a link the renderer asks for: web pages and
// mail. Anything else (file:, javascript:, a custom protocol handler) could
// start a program, so it is refused.
const safeSchemes = new Set(['http:', 'https:', 'mailto:']);

export function isSafeExternalUrl(url: unknown): url is string {
  if (typeof url !== 'string') return false;
  try {
    return safeSchemes.has(new URL(url.trim()).protocol);
  } catch {
    return false;
  }
}
