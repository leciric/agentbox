package incus

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"time"

	incusclient "github.com/lxc/incus/v6/client"
	"github.com/lxc/incus/v6/shared/api"
)

// Each operation below is one incus command, made through the API the way
// that command makes it, down to its error messages where AgentBox reads them
// ("not found", "already exists"). The comment on each names the command.

// Copy makes a new instance from another, or from one of its snapshots when
// source is "instance/snapshot": `incus copy source name`. Like the command,
// it keeps the source's configuration and devices but not its volatile keys,
// and a copied instance brings its snapshots along.
func (c Client) Copy(ctx context.Context, source, name string) error {
	return c.do(ctx, []string{"copy", source, name}, func(s incusclient.InstanceServer) error {
		var op incusclient.RemoteOperation
		if instance, snapshot, ok := strings.Cut(source, "/"); ok {
			entry, _, err := s.GetInstanceSnapshot(instance, snapshot)
			if err != nil {
				return err
			}
			entry.Config = copiedConfig(entry.Config)
			entry.Ephemeral = false
			op, err = s.CopyInstanceSnapshot(s, instance, *entry, &incusclient.InstanceSnapshotCopyArgs{
				Name: name, Mode: "pull", Live: true,
			})
			if err != nil {
				return err
			}
		} else {
			entry, _, err := s.GetInstance(source)
			if err != nil {
				return err
			}
			entry.Config = copiedConfig(entry.Config)
			entry.Ephemeral = false
			op, err = s.CopyInstance(s, *entry, &incusclient.InstanceCopyArgs{
				Name: name, Mode: "pull", Live: true,
			})
			if err != nil {
				return err
			}
		}
		return waitRemote(ctx, op)
	})
}

// copiedConfig is what `incus copy` keeps of a source's configuration: all of
// it but the volatile keys, except the two it carries across on purpose.
func copiedConfig(config map[string]string) map[string]string {
	kept := make(map[string]string, len(config))
	for k, v := range config {
		if !strings.HasPrefix(k, "volatile.") || k == "volatile.apply_nvram" || k == "volatile.base_image" {
			kept[k] = v
		}
	}
	return kept
}

// waitRemote waits for a copy. Its operation has no WaitContext: it is waited
// for in the background, and ctx gives up on it without cancelling it, as
// killing `incus copy` never cancelled the copy Incus was making.
func waitRemote(ctx context.Context, op incusclient.RemoteOperation) error {
	done := make(chan error, 1)
	go func() { done <- op.Wait() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Delete deletes an instance, stopping it first if it runs:
// `incus delete --force name`.
func (c Client) Delete(ctx context.Context, name string) error {
	return c.do(ctx, []string{"delete", "--force", name}, func(s incusclient.InstanceServer) error {
		inst, _, err := s.GetInstance(name)
		if err != nil {
			return fmt.Errorf("Failed checking instance %s exists: %w", name, err)
		}
		if inst.StatusCode != 0 && inst.StatusCode != api.Stopped {
			op, err := s.UpdateInstanceState(name, api.InstanceStatePut{Action: "stop", Timeout: -1, Force: true}, "")
			if err := wait(ctx, op, err); err != nil {
				return fmt.Errorf("Stopping the instance %s failed: %s", name, err)
			}
			if inst.Ephemeral {
				return nil // stopping it deleted it
			}
		}
		op, err := s.DeleteInstance(name)
		if err := wait(ctx, op, err); err != nil {
			project := ""
			if info, infoErr := s.GetConnectionInfo(); infoErr == nil {
				project = info.Project
			}
			return fmt.Errorf("Failed deleting instance %s in project %q: %w", name, project, err)
		}
		return nil
	})
}

// Start starts an instance: `incus start name`. Like the command, it
// unfreezes a paused one.
func (c Client) Start(ctx context.Context, name string) error {
	return c.action(ctx, []string{"start", name}, name, api.InstanceStatePut{Action: "start", Timeout: -1})
}

// Stop shuts an instance down cleanly, waiting as long as that takes:
// `incus stop name`.
func (c Client) Stop(ctx context.Context, name string) error {
	return c.action(ctx, []string{"stop", name}, name, api.InstanceStatePut{Action: "stop", Timeout: -1})
}

// StopWithin shuts an instance down cleanly, failing after timeout:
// `incus stop name --timeout N`.
func (c Client) StopWithin(ctx context.Context, name string, timeout time.Duration) error {
	secs := int(timeout / time.Second)
	return c.action(ctx, []string{"stop", name, "--timeout", strconv.Itoa(secs)}, name,
		api.InstanceStatePut{Action: "stop", Timeout: secs})
}

// ForceStop kills an instance: `incus stop name --force`.
func (c Client) ForceStop(ctx context.Context, name string) error {
	return c.action(ctx, []string{"stop", name, "--force"}, name, api.InstanceStatePut{Action: "stop", Timeout: -1, Force: true})
}

// Pause freezes an instance's processes: `incus pause name`.
func (c Client) Pause(ctx context.Context, name string) error {
	return c.action(ctx, []string{"pause", name}, name, api.InstanceStatePut{Action: "freeze", Timeout: -1})
}

// Resume thaws a paused instance: `incus resume name`.
func (c Client) Resume(ctx context.Context, name string) error {
	return c.action(ctx, []string{"resume", name}, name, api.InstanceStatePut{Action: "unfreeze", Timeout: -1})
}

// action changes an instance's state as `incus start/stop/pause/resume` do,
// and fails as they do, pointing at the instance's log.
func (c Client) action(ctx context.Context, args []string, name string, req api.InstanceStatePut) error {
	return c.do(ctx, args, func(s incusclient.InstanceServer) error {
		if req.Action == "start" {
			current, _, err := s.GetInstance(name)
			if err != nil {
				return err
			}
			if current.StatusCode == api.Frozen {
				req.Action = "unfreeze"
			} else if current.Stateful {
				req.Stateful = true // restore the state it was stopped with
			}
		}
		op, err := s.UpdateInstanceState(name, req, "")
		if err != nil {
			return err
		}
		// The command looks at the operation once before it waits on it, and
		// one that has already failed by then — "The instance is already
		// running" — is reported as it is, without the pointer to the log.
		if err := op.Refresh(); err == nil && op.Get().StatusCode.IsFinal() {
			if msg := op.Get().Err; msg != "" {
				return errors.New(msg)
			}
			return nil
		}
		if err := wait(ctx, op, nil); err != nil {
			if ctx.Err() != nil {
				return err
			}
			return fmt.Errorf("%w\nTry `incus info --show-log %s` for more info", err, name)
		}
		return nil
	})
}

// Rename renames a stopped instance: `incus rename name to`.
func (c Client) Rename(ctx context.Context, name, to string) error {
	return c.do(ctx, []string{"rename", name, to}, func(s incusclient.InstanceServer) error {
		op, err := s.RenameInstance(name, api.InstancePost{Name: to})
		return wait(ctx, op, err)
	})
}

// CreateSnapshot snapshots an instance: `incus snapshot create name snapshot`.
func (c Client) CreateSnapshot(ctx context.Context, name, snapshot string) error {
	return c.do(ctx, []string{"snapshot", "create", name, snapshot}, func(s incusclient.InstanceServer) error {
		op, err := s.CreateInstanceSnapshot(name, api.InstanceSnapshotsPost{Name: snapshot})
		return wait(ctx, op, err)
	})
}

// RestoreSnapshot puts an instance back to one of its snapshots:
// `incus snapshot restore name snapshot`.
func (c Client) RestoreSnapshot(ctx context.Context, name, snapshot string) error {
	return c.do(ctx, []string{"snapshot", "restore", name, snapshot}, func(s incusclient.InstanceServer) error {
		op, err := s.UpdateInstance(name, api.InstancePut{Restore: snapshot}, "")
		return wait(ctx, op, err)
	})
}

// DeleteSnapshot deletes one of an instance's snapshots:
// `incus snapshot delete name snapshot`.
func (c Client) DeleteSnapshot(ctx context.Context, name, snapshot string) error {
	return c.do(ctx, []string{"snapshot", "delete", name, snapshot}, func(s incusclient.InstanceServer) error {
		op, err := s.DeleteInstanceSnapshot(name, snapshot)
		return wait(ctx, op, err)
	})
}

// SetConfig sets configuration keys on an instance, each pair "key=value":
// `incus config set name key=value...`.
func (c Client) SetConfig(ctx context.Context, name string, pairs ...string) error {
	args := append([]string{"config", "set", name}, pairs...)
	return c.do(ctx, args, func(s incusclient.InstanceServer) error {
		values := make(map[string]string, len(pairs))
		for _, pair := range pairs {
			key, value, ok := strings.Cut(pair, "=")
			if !ok {
				return fmt.Errorf("Invalid key=value configuration: %s", pair)
			}
			values[key] = value
		}
		return c.updateInstance(ctx, s, name, func(put *api.InstancePut) error {
			if put.Config == nil {
				put.Config = map[string]string{}
			}
			maps.Copy(put.Config, values)
			return nil
		})
	})
}

// UnsetConfig removes a configuration key from an instance:
// `incus config unset name key`.
func (c Client) UnsetConfig(ctx context.Context, name, key string) error {
	return c.do(ctx, []string{"config", "unset", name, key}, func(s incusclient.InstanceServer) error {
		return c.updateInstance(ctx, s, name, func(put *api.InstancePut) error {
			if _, ok := put.Config[key]; !ok {
				return fmt.Errorf("Can't unset key '%s', it's not currently set", key)
			}
			delete(put.Config, key)
			return nil
		})
	})
}

// AddDevice adds a device to an instance, its options each "key=value":
// `incus config device add name device type key=value...`.
func (c Client) AddDevice(ctx context.Context, name, device, kind string, options ...string) error {
	args := append([]string{"config", "device", "add", name, device, kind}, options...)
	return c.do(ctx, args, func(s incusclient.InstanceServer) error {
		dev := map[string]string{"type": kind}
		for _, option := range options {
			key, value, ok := strings.Cut(option, "=")
			if !ok {
				return fmt.Errorf("No value found in %q", option)
			}
			dev[key] = value
		}
		return c.updateInstance(ctx, s, name, func(put *api.InstancePut) error {
			if _, ok := put.Devices[device]; ok {
				return errors.New("The device already exists")
			}
			if put.Devices == nil {
				put.Devices = map[string]map[string]string{}
			}
			put.Devices[device] = dev
			return nil
		})
	})
}

// RemoveDevice removes a device from an instance:
// `incus config device remove name device`.
func (c Client) RemoveDevice(ctx context.Context, name, device string) error {
	return c.do(ctx, []string{"config", "device", "remove", name, device}, func(s incusclient.InstanceServer) error {
		return c.updateInstance(ctx, s, name, func(put *api.InstancePut) error {
			if _, ok := put.Devices[device]; !ok {
				return fmt.Errorf("Device “%s” doesn't exist", device)
			}
			delete(put.Devices, device)
			return nil
		})
	})
}

// updateInstance changes an instance's own configuration and devices, guarded
// by its ETag, as `incus config` does.
func (c Client) updateInstance(ctx context.Context, s incusclient.InstanceServer, name string, change func(*api.InstancePut) error) error {
	inst, etag, err := s.GetInstance(name)
	if err != nil {
		return err
	}
	put := inst.Writable()
	if err := change(&put); err != nil {
		return err
	}
	op, err := s.UpdateInstance(name, put, etag)
	return wait(ctx, op, err)
}

// HasProfile reports whether a profile exists: `incus profile show name`
// succeeding.
func (c Client) HasProfile(ctx context.Context, name string) (bool, error) {
	err := c.do(ctx, []string{"profile", "show", name}, func(s incusclient.InstanceServer) error {
		_, _, err := s.GetProfile(name)
		return err
	})
	if err != nil {
		if c.cli() || isNotFound(errors.Unwrap(err)) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// CreateProfile makes an empty profile: `incus profile create name`.
func (c Client) CreateProfile(ctx context.Context, name string) error {
	return c.do(ctx, []string{"profile", "create", name}, func(s incusclient.InstanceServer) error {
		return s.CreateProfile(api.ProfilesPost{Name: name})
	})
}

// SetProfileConfig sets configuration keys on a profile, each pair
// "key=value": `incus profile set name key=value...`.
func (c Client) SetProfileConfig(ctx context.Context, name string, pairs ...string) error {
	args := append([]string{"profile", "set", name}, pairs...)
	return c.do(ctx, args, func(s incusclient.InstanceServer) error {
		profile, etag, err := s.GetProfile(name)
		if err != nil {
			return err
		}
		put := profile.Writable()
		if put.Config == nil {
			put.Config = map[string]string{}
		}
		for _, pair := range pairs {
			key, value, ok := strings.Cut(pair, "=")
			if !ok {
				return fmt.Errorf("Invalid key=value configuration: %s", pair)
			}
			put.Config[key] = value
		}
		return s.UpdateProfile(name, put, etag)
	})
}

// Init makes an instance from an image, with the given profiles:
// `incus init image name --profile p...`. It always runs the incus command,
// which resolves image remotes ("images:debian/13") from the user's own
// configuration, the way the image build has always found its image.
func (c Client) Init(ctx context.Context, image, name string, profiles ...string) error {
	args := []string{"init", image, name}
	for _, p := range profiles {
		args = append(args, "--profile", p)
	}
	_, err := c.run(ctx, args...)
	return err
}
