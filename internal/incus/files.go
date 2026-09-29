package incus

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	incusclient "github.com/lxc/incus/v7/client"
	"github.com/pkg/sftp"
)

// PullFile copies a file out of an instance to target on the host, with the
// file's mode: `incus file pull name/path target`. A symlink is followed; a
// target that is a directory gets the file inside it.
func (c Client) PullFile(ctx context.Context, name, file, target string) error {
	return c.do(ctx, []string{"file", "pull", name + file, target}, func(s incusclient.InstanceServer) error {
		return withSFTP(ctx, s, name, func(conn *sftp.Client) error {
			info, err := conn.Stat(file)
			if err != nil {
				return err
			}
			if st, err := os.Stat(target); err == nil && st.IsDir() {
				target = filepath.Join(target, path.Base(file))
			}
			return pullFile(conn, file, target, info.Mode())
		})
	})
}

// PullDir copies a directory out of an instance into dir on the host, where
// it lands under its own name: `incus file pull -r name/path dir`. Symlinks
// inside it are copied as symlinks.
func (c Client) PullDir(ctx context.Context, name, from, dir string) error {
	return c.do(ctx, []string{"file", "pull", "-r", name + from, dir}, func(s incusclient.InstanceServer) error {
		return withSFTP(ctx, s, name, func(conn *sftp.Client) error {
			target := dir
			if _, err := os.Stat(dir); err == nil {
				target = filepath.Join(dir, path.Base(from))
			}
			return pullTree(conn, from, target)
		})
	})
}

func pullTree(conn *sftp.Client, from, target string) error {
	info, err := conn.Lstat(from)
	if err != nil {
		return err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		link, err := conn.ReadLink(from)
		if err != nil {
			return err
		}
		return os.Symlink(link, target)
	case info.IsDir():
		if err := os.Mkdir(target, info.Mode().Perm()); err != nil && !os.IsExist(err) {
			return err
		}
		entries, err := conn.ReadDir(from)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := pullTree(conn, path.Join(from, entry.Name()), filepath.Join(target, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	default:
		return pullFile(conn, from, target, info.Mode())
	}
}

// sftpChunk is how much pullFile asks an instance's SFTP server for at a
// time: no more than a packet, whatever the client's maximum, since sftp's
// own default is 32 KiB and Incus's is 128 KiB.
const sftpChunk = 32 * 1024

func pullFile(conn *sftp.Client, from, target string, mode os.FileMode) error {
	src, err := conn.Open(from)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.Create(target)
	if err != nil {
		return err
	}
	// Not io.Copy(dst, src): that hands the copy to src.WriteTo, which reads
	// a large file in concurrent maxPacket-sized requests and trusts each to
	// come back whole. Incus asks for 128 KiB packets and its server answers
	// with 32 KiB, so every 128 KiB kept its first 32 and lost the rest: a
	// recording came out a quarter of its size, and played as black. Reads
	// of at most a packet are sent one at a time, and pkg/sftp asks again
	// for whatever a short answer left out; sftp's ReadAt, for a larger
	// buffer, takes a short answer for the end of the file instead.
	if _, err := io.CopyBuffer(dst, struct{ io.Reader }{src}, make([]byte, sftpChunk)); err != nil {
		_ = dst.Close()
		return err
	}
	if err := dst.Close(); err != nil {
		return err
	}
	return os.Chmod(target, mode.Perm())
}

// withSFTP opens an SFTP session into the instance, which is how the incus
// command moves files, and closes it when call returns or ctx ends.
func withSFTP(ctx context.Context, s incusclient.InstanceServer, name string, call func(*sftp.Client) error) error {
	conn, err := s.GetInstanceFileSFTP(name)
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer func() { _ = conn.Close() }()
	if err := call(conn); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return err
	}
	return nil
}

// PushFile copies a file from the host into an instance, with mode:
// `incus file push local name/path --mode 0755`. It goes over SFTP, as the
// command does: a target that is a directory gets the file inside it, a new
// file is owned by the host file's owner, and one that was there keeps its
// owner.
func (c Client) PushFile(ctx context.Context, local, name, file string, mode os.FileMode) error {
	args := []string{"file", "push", local, name + file, "--mode", fmt.Sprintf("%04o", mode.Perm())}
	return c.do(ctx, args, func(s incusclient.InstanceServer) error {
		src, err := os.Open(local)
		if err != nil {
			return fmt.Errorf("Failed to open source file %q: %v", local, err)
		}
		defer func() { _ = src.Close() }()
		info, err := src.Stat()
		if err != nil {
			return err
		}
		return withSFTP(ctx, s, name, func(conn *sftp.Client) error {
			target := path.Clean(file)
			if st, err := conn.Lstat(target); err == nil && st.IsDir() {
				target = path.Join(target, filepath.Base(local))
			}
			_, err := conn.Stat(target)
			exists := err == nil
			dst, err := conn.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
			if err != nil {
				return fmt.Errorf("Failed to open target file %q: %w", target, err)
			}
			defer func() { _ = dst.Close() }()
			// Not io.Copy(dst, src): that hands the copy to src.WriteTo, which reads
	// a large file in concurrent maxPacket-sized requests and trusts each to
	// come back whole. Incus asks for 128 KiB packets and its server answers
	// with 32 KiB, so every 128 KiB kept its first 32 and lost the rest: a
	// recording came out a quarter of its size, and played as black. Reads
	// of at most a packet are sent one at a time, and pkg/sftp asks again
	// for whatever a short answer left out; sftp's ReadAt, for a larger
	// buffer, takes a short answer for the end of the file instead.
	if _, err := io.CopyBuffer(dst, struct{ io.Reader }{src}, make([]byte, sftpChunk)); err != nil {
				return err
			}
			if !exists {
				uid, gid := owner(info)
				if err := conn.Chown(target, int(uid), int(gid)); err != nil {
					return err
				}
			}
			return conn.Chmod(target, mode.Perm())
		})
	})
}
