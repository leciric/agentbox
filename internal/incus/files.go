package incus

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	incusclient "github.com/lxc/incus/v6/client"
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
	if _, err := io.Copy(dst, src); err != nil {
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
			if _, err := io.Copy(dst, src); err != nil {
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
