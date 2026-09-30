// openLatestRelease is what "Update available", and Settings' links to a new
// release, open: the update channel's latest release as the daemon finds it on
// GitHub at the moment of the click (GET /v1/update/release), not the page the
// last daily check pinned, which releases a few hours apart left behind. When
// the daemon can't say, the pinned page is still better than nothing.
export async function openLatestRelease(
  latest: () => Promise<{ url: string }>,
  open: (url: string) => unknown,
  pinned: string,
): Promise<void> {
  let url = pinned;
  try {
    url = (await latest()).url || pinned;
  } catch {
    // The daemon or GitHub unreachable: the pinned page.
  }
  await open(url);
}
