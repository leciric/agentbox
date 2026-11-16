// The policy a chat's framed page is served under (main/renders.ts), here so
// the preview harness (renderer/dev) can frame pages under it too.

// renderPolicy is a page's Content-Security-Policy, with or without network.
export function renderPolicy(network: boolean): string {
  const net = network ? ' https:' : '';
  return [
    "default-src 'none'",
    `script-src 'unsafe-inline' 'unsafe-eval'${net}`,
    `style-src 'unsafe-inline'${net}`,
    `img-src data: blob:${net}`,
    `font-src data:${net}`,
    `media-src data: blob:${net}`,
    `connect-src ${network ? 'https:' : "'none'"}`,
    "form-action 'none'",
    "base-uri 'none'",
    "object-src 'none'",
    'sandbox allow-scripts',
  ].join('; ');
}
