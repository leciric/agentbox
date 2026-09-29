package agent

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"agentbox/internal/image"
)

// The lead opens pull requests and reads CI with the GitHub CLI, as agents do,
// but in VM mode the host has none, and elsewhere only if the user happens to
// have installed it. So hostTools fetches it too, on first use, into AgentBox's
// tools directory: the official release tarball, at the version the base image
// pins, checked against the release's own checksums.
//
// Unlike Claude Code it is not something the chat can't do without: git reaches
// GitHub through the credential helper configureLead writes, whether or not gh
// is there. A failed install is logged and the chat starts without it.

// ghVersion is the GitHub CLI the lead gets: the one tools.txt pins for agents.
var ghVersion = pinnedVersion(image.Pin("gh"))

// ghReleases is where the GitHub CLI's release assets are downloaded from.
const ghReleases = "https://github.com/cli/cli/releases/download"

// ghInstallTimeout bounds the download, shorter than hostInstallTimeout: the
// chat waits for it, and can start without it. The tarball is about 15 MiB.
const ghInstallTimeout = 5 * time.Minute

// ghDir holds AgentBox's own GitHub CLI, one directory per version, so moving
// the pin on installs the new one rather than keeping the old one forever.
func (m *Manager) ghDir() string { return filepath.Join(m.toolsHome(), "gh", ghVersion) }

func (m *Manager) ghPath() string { return filepath.Join(m.ghDir(), "bin", "gh") }

// ensureGH returns the path of a GitHub CLI the lead can run: AgentBox's own,
// then one on the host's PATH, and otherwise it downloads AgentBox's own.
func (m *Manager) ensureGH(ctx context.Context, status func(string)) (string, error) {
	own := m.ghPath()
	if usable(own) {
		return own, nil
	}
	if path := onPath("gh"); path != "" {
		return path, nil
	}
	ctx, cancel := context.WithTimeout(ctx, ghInstallTimeout)
	defer cancel()
	if err := m.installGH(ctx, status); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("installing the GitHub CLI took longer than %s: check this machine's connection", ghInstallTimeout)
		}
		return "", fmt.Errorf("installing the GitHub CLI %s: %w", ghVersion, err)
	}
	return own, nil
}

// ghAsset is the name of the release tarball for this machine.
func ghAsset() (string, error) {
	switch runtime.GOARCH {
	case "amd64", "arm64":
		return fmt.Sprintf("gh_%s_linux_%s.tar.gz", ghVersion, runtime.GOARCH), nil
	}
	return "", fmt.Errorf("the GitHub CLI publishes no linux/%s build AgentBox knows", runtime.GOARCH)
}

// installGH downloads the release tarball, checks it against the release's
// checksums, and puts its bin/gh in ghDir. Nothing lands there until all of
// that has worked, so an interrupted install leaves nothing half done.
func (m *Manager) installGH(ctx context.Context, status func(string)) error {
	asset, err := ghAsset()
	if err != nil {
		return err
	}
	base := m.ghReleases
	if base == "" {
		base = ghReleases
	}
	release := fmt.Sprintf("%s/v%s/", strings.TrimSuffix(base, "/"), ghVersion)

	status("Downloading the GitHub CLI, once for this machine")
	sums, err := ghChecksums(ctx, release+fmt.Sprintf("gh_%s_checksums.txt", ghVersion))
	if err != nil {
		return err
	}
	want, ok := sums[asset]
	if !ok {
		return fmt.Errorf("the release's checksums list no %s", asset)
	}

	parent := filepath.Dir(m.ghDir())
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	tarball, err := os.CreateTemp(parent, ".download-*.tar.gz")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tarball.Name()) }()
	defer func() { _ = tarball.Close() }()
	got, err := ghDownload(ctx, release+asset, tarball, status)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%s doesn't match the release's checksum (got sha256 %s, want %s)", asset, got, want)
	}

	staging, err := os.MkdirTemp(parent, ".staging-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if _, err := tarball.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if err := extractGH(tarball, filepath.Join(staging, "bin", "gh")); err != nil {
		return fmt.Errorf("unpacking %s: %w", asset, err)
	}
	_ = os.RemoveAll(m.ghDir())
	if err := os.Rename(staging, m.ghDir()); err != nil {
		return err
	}
	// Versions an older AgentBox installed are no use to anything now.
	if entries, err := os.ReadDir(parent); err == nil {
		for _, e := range entries {
			if e.IsDir() && e.Name() != ghVersion && !strings.HasPrefix(e.Name(), ".") {
				_ = os.RemoveAll(filepath.Join(parent, e.Name()))
			}
		}
	}
	return nil
}

// ghChecksums reads a release's checksums file: "<sha256>  <asset>" a line.
func ghChecksums(ctx context.Context, url string) (map[string]string, error) {
	body, err := ghGet(ctx, url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	sums := map[string]string{}
	scanner := bufio.NewScanner(io.LimitReader(body, 1<<20))
	for scanner.Scan() {
		if fields := strings.Fields(scanner.Text()); len(fields) == 2 {
			sums[fields[1]] = strings.ToLower(fields[0])
		}
	}
	return sums, scanner.Err()
}

// ghDownload writes url to w and returns its sha256, reporting how far it has
// got through status as it goes.
func ghDownload(ctx context.Context, url string, w io.Writer, status func(string)) (string, error) {
	body, err := ghGet(ctx, url)
	if err != nil {
		return "", err
	}
	defer func() { _ = body.Close() }()
	hash := sha256.New()
	progress := &downloadProgress{total: body.size, status: status}
	if _, err := io.Copy(io.MultiWriter(w, hash, progress), body); err != nil {
		return "", fmt.Errorf("downloading %s: %w", path.Base(url), err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type sizedBody struct {
	io.ReadCloser
	size int64 // -1 when the server didn't say
}

func ghGet(ctx context.Context, url string) (*sizedBody, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		if offline(err.Error()) {
			return nil, errors.New("this machine seems to be offline")
		}
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("downloading %s: %s", path.Base(url), resp.Status)
	}
	return &sizedBody{ReadCloser: resp.Body, size: resp.ContentLength}, nil
}

// downloadProgress updates the chat's status line once per MiB downloaded.
type downloadProgress struct {
	done, total int64
	shown       int64
	status      func(string)
}

func (p *downloadProgress) Write(b []byte) (int, error) {
	p.done += int64(len(b))
	if mib := p.done >> 20; mib > p.shown {
		p.shown = mib
		if p.total > 0 {
			p.status(fmt.Sprintf("Downloading the GitHub CLI, once for this machine: %d of %d MiB", mib, (p.total+1<<20-1)>>20))
		} else {
			p.status(fmt.Sprintf("Downloading the GitHub CLI, once for this machine: %d MiB", mib))
		}
	}
	return len(b), nil
}

// extractGH writes the tarball's bin/gh, which sits under a top directory named
// after the release, to dst. The rest (the manual, the license) the lead has
// no use for.
func extractGH(r io.Reader, dst string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("it has no bin/gh")
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || !strings.HasSuffix(h.Name, "/bin/gh") || strings.Count(h.Name, "/") != 2 {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(f, tr); err != nil {
			_ = f.Close()
			return err
		}
		return f.Close()
	}
}
