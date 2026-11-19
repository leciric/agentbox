// parseHubLink reads what someone pasted to connect to a hub: its address, or
// a setup link (https://hub.example.com/hub/setup#abx_l_…), whose secret makes
// them the hub's owner or signs the owner in. It is the CLI's
// api.ParseHubLink: an address with no scheme is https, and only the origin
// is kept. null when it isn't a hub's address.
export function parseHubLink(input: string): { url: string; secret?: string } | null {
  let text = input.trim();
  if (!text) return null;
  if (!text.includes('://')) text = `https://${text}`;
  let u: URL;
  try {
    u = new URL(text);
  } catch {
    return null;
  }
  if ((u.protocol !== 'https:' && u.protocol !== 'http:') || !u.host) return null;
  const fragment = decodeURIComponent(u.hash.slice(1));
  return fragment.startsWith('abx_l_') ? { url: u.origin, secret: fragment } : { url: u.origin };
}
