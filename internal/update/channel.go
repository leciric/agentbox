package update

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/mod/semver"
)

// The update channels. Stable is every release made by merging
// release-please's PR; nightly adds the prereleases .github/workflows/nightly.yml
// builds from that PR while it carries the nightly label.
const (
	ChannelStable  = "stable"
	ChannelNightly = "nightly"
)

// DefaultReleasesURL is the list of releases unless AGENTBOX_RELEASES_URL says
// otherwise: releases.json in the Cloudflare R2 bucket the release and nightly
// workflows publish to (scripts/r2-publish.sh), newest first, in the shape of
// GitHub's release list. The repository is private, so GitHub's own list and
// downloads answer nobody but its collaborators. agentbox.linting.dev only
// ever answers with a stable release, so the nightly channel asks here as well.
const DefaultReleasesURL = "https://downloads.agentbox.linting.dev/releases.json"

// ReleasePage is the page of the release tagged tag, beside the release list
// at base (DefaultReleasesURL when empty): releases/<tag>/index.html, which
// scripts/r2-publish.sh writes into the directory holding the release's
// assets. The directory itself is the AppImage's update feed
// (desktop/src/shared/appUpdate.ts). An R2 bucket serves no directory
// listing or index, so the page is named in full.
func ReleasePage(base, tag string) string {
	list, err := url.Parse(cmp.Or(base, DefaultReleasesURL))
	if err != nil {
		return ""
	}
	page := list.ResolveReference(&url.URL{Path: "releases/" + url.PathEscape(tag) + "/index.html"})
	return page.String()
}

// nightlyVersion is what the nightly workflow calls a build:
// <next release>-nightly.<YYYYMMDD>.<run>. Semver orders two of them by date,
// then by run, and puts each before the release it leads up to.
var nightlyVersion = regexp.MustCompile(`^v?\d+\.\d+\.\d+-nightly\.\d{8}\.\d+$`)

// releaseTag is a tag the nightly channel may offer: a stable release's
// vX.Y.Z or a nightly's. The repository has other releases too (the agents'
// base image, image-<version>), which it never offers.
var releaseTag = regexp.MustCompile(`^v\d+\.\d+\.\d+(-nightly\.\d{8}\.\d+)?$`)

// IsNightly says whether version is a nightly build.
func IsNightly(version string) bool {
	return nightlyVersion.MatchString(strings.TrimSpace(version))
}

// DefaultChannel is the channel of an installation that never chose one: the
// one its build came from, so installing a nightly keeps it on nightlies.
func DefaultChannel(version string) string {
	if IsNightly(version) {
		return ChannelNightly
	}
	return ChannelStable
}

// ValidChannel says whether c is one of the channels.
func ValidChannel(c string) bool {
	return c == ChannelStable || c == ChannelNightly
}

// Offer picks what to offer an installation of current on channel, given the
// latest stable release and, on the nightly channel, the latest nightly (either
// may be empty, when it isn't known). ok is false when there is nothing to
// offer.
//
// On the nightly channel it is whichever of the two is newer, when that is
// newer than current: a stable release outranks the nightlies that led up to
// it. On the stable channel it is the latest stable when that is newer — or,
// when current is a nightly, whenever it isn't current itself: switching back
// from nightly to stable offers the latest stable even though its version is
// lower than the nightly's.
func Offer(channel, current string, stable, nightly Latest) (Latest, bool) {
	if channel == ChannelNightly {
		best := stable
		if !valid(best.Version) || Newer(nightly.Version, best.Version) {
			best = nightly
		}
		return best, Newer(best.Version, current)
	}
	if Newer(stable.Version, current) {
		return stable, true
	}
	if IsNightly(current) && valid(stable.Version) && !IsNightly(stable.Version) && canonical(stable.Version) != canonical(current) {
		return stable, true
	}
	return Latest{}, false
}

func valid(v string) bool { return v != "" && semver.IsValid(canonical(v)) }

// LatestRelease asks the release list at base (DefaultReleasesURL when
// empty) for the newest published release of channel: on the stable channel
// the newest vX.Y.Z that isn't a prerelease, on the nightly channel that or a
// nightly, whichever is newer, with the files it offers for download. Drafts
// and any other tag are skipped. It sends no ID and nothing about the machine:
// the request is a plain GET of a public page.
func LatestRelease(ctx context.Context, base, channel string) (Latest, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, cmp.Or(base, DefaultReleasesURL), nil)
	if err != nil {
		return Latest{}, err
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return Latest{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return Latest{}, fmt.Errorf("release list: %s", resp.Status)
	}
	var releases []struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 4<<20)).Decode(&releases); err != nil {
		return Latest{}, err
	}
	var best Latest
	for _, rel := range releases {
		if rel.Draft || !releaseTag.MatchString(rel.Tag) {
			continue
		}
		if channel != ChannelNightly && (rel.Prerelease || IsNightly(rel.Tag)) {
			continue
		}
		if v := strings.TrimPrefix(rel.Tag, "v"); best.Version == "" || Newer(v, best.Version) {
			best = Latest{Version: v, URL: ReleasePage(base, rel.Tag)}
			for _, a := range rel.Assets {
				best.Assets = append(best.Assets, Asset(a))
			}
		}
	}
	if best.Version == "" {
		return Latest{}, fmt.Errorf("release list: no release")
	}
	return best, nil
}
