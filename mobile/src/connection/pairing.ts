// What pairing reads from the QR code AgentBox shows on the computer
// (internal/daemon/lan.go, addLANPairing): the address the daemon serves
// phones on, over the local network or the Cloudflare tunnel (lantunnel.go),
// with a one-time secret in its fragment, `http://192.168.1.20:7780/#pair=…`.
// The web phone app opens that page; this app posts the secret itself. After
// t3code's apps/mobile/src/features/connection/pairing.ts (parsePairingUrl).

export interface PairingLink {
  // origin is where the daemon answers phones: scheme, host and port.
  origin: string;
  secret: string;
}

// parsePairingLink reads a pairing link, scanned or pasted, or null when it
// isn't one.
export function parsePairingLink(raw: string): PairingLink | null {
  const text = raw.trim();
  let url: URL;
  try {
    url = new URL(text);
  } catch {
    return null;
  }
  if (url.protocol !== 'http:' && url.protocol !== 'https:') return null;
  const secret = /(?:^#|&)pair=([^&]+)/.exec(url.hash)?.[1];
  if (!secret) return null;
  return { origin: url.origin, secret: decodeURIComponent(secret) };
}

// isTailnet says origin is an address in a tailnet, where a hub on Tailscale
// puts its machines (internal/tunnel/tailscale.go): a MagicDNS name under
// ts.net, or a Tailscale address, 100.64.0.0/10. A phone reaches it only with
// Tailscale on, in that tailnet.
export function isTailnet(origin: string): boolean {
  let host: string;
  try {
    host = new URL(origin).hostname.toLowerCase();
  } catch {
    return false;
  }
  if (host.endsWith('.ts.net')) return true;
  const ip = /^100\.(\d{1,3})\.\d{1,3}\.\d{1,3}$/.exec(host);
  return !!ip && Number(ip[1]) >= 64 && Number(ip[1]) <= 127;
}

// isPlainHTTP says the phone talks to the daemon unencrypted: on the local
// network, where the daemon has no certificate. The tunnel is https.
export const isPlainHTTP = (origin: string) => origin.startsWith('http:');
