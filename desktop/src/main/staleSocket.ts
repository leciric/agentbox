// A keep-alive agent reuses pooled sockets, and the far end (the daemon, or the
// forward in front of it) may have closed one while it sat idle. Node reports
// that as ECONNRESET or EPIPE on a request that reused the socket, and says to
// send it again on a fresh one. Only reads are repeated: a write may have
// reached the daemon before the socket died.

export function staleSocketRetry(method: string, err: unknown, reusedSocket: boolean): boolean {
  if (!reusedSocket) return false;
  if (method !== 'GET' && method !== 'HEAD') return false;
  const code = (err as NodeJS.ErrnoException | undefined)?.code;
  return code === 'ECONNRESET' || code === 'EPIPE';
}
