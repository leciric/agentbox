// phoneName is what the phone is listed as on the computer, as well as its
// browser says it.
export function phoneName(ua = navigator.userAgent, touchPoints = navigator.maxTouchPoints): string {
  if (/iPhone/.test(ua)) return 'iPhone';
  if (/iPad/.test(ua) || (/Macintosh/.test(ua) && touchPoints > 1)) return 'iPad';
  const android = /Android[^;)]*;\s*([^;)]+?)(?:\s+Build\/[^;)]*)?[;)]/.exec(ua);
  if (android) {
    const model = android[1].trim();
    return model && model !== 'K' && !/^(Linux|U|wv)$/.test(model) ? model : 'Android phone';
  }
  if (/Firefox\//.test(ua)) return 'Firefox';
  if (/Chrome\//.test(ua)) return 'Chrome';
  if (/Safari\//.test(ua)) return 'Safari';
  return 'Phone';
}
