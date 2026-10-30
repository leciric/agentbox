// percentOf reads the percentage a base image build's progress starts with
// ("40% of 1800 MB", T.ImageStatus.progress), or null when it says none.
export function percentOf(progress?: string): number | null {
  const m = progress?.match(/^(\d{1,3})%/);
  return m ? Math.min(100, Number(m[1])) : null;
}
