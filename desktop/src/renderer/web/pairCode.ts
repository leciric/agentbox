// pairSecretOf finds the one-time pairing secret (internal/daemon/lan.go's
// addLANPairing) in what a phone was given: the link a QR code carries, which
// has it in its fragment, the link copied from the computer, or the code on
// its own. null when there's none.
export function pairSecretOf(text: string): string | null {
  const s = text.trim();
  const inLink = /[#&]pair=([A-Za-z0-9_-]+)/.exec(s)?.[1];
  if (inLink) return inLink;
  return /^[A-Za-z0-9_-]{16,64}$/.test(s) ? s : null;
}
