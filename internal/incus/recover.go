package incus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	incusclient "github.com/lxc/incus/v7/client"
	"github.com/lxc/incus/v7/shared/api"
)

// An Incus stopped in the middle of copying or deleting an instance can lose
// track of it and keep its storage: `incus list` doesn't show it, deleting it
// says it isn't there, and making another of that name says its volume
// already exists. `incus admin recover` takes such volumes back, and this is
// what it asks Incus, after which the instance can be deleted as any other.

// recoverPost is what /internal/recover/validate and /import are asked.
type recoverPost struct {
	Pools              []api.StoragePoolsPost `json:"pools"`
	DependencySelfHeal bool                   `json:"dependencySelfHeal"`
}

// recoverResult is what /internal/recover/validate answers.
type recoverResult struct {
	UnknownVolumes []struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		Project string `json:"project"`
		Pool    string `json:"pool"`
	}
	DependencyErrors []string
}

// ErrCantRecover is why RecoverLeftovers didn't take back a lost instance it
// found: one a person has to look at, unlike Incus not answering.
var ErrCantRecover = errors.New("can't take it back")

// RecoverLeftovers makes Incus take back the instances among names whose
// storage it has lost track of, and returns which it took back. Incus takes
// back every lost volume at once, so it is asked only when each one is among
// names: the others aren't AgentBox's to bring back.
func (c Client) RecoverLeftovers(ctx context.Context, names ...string) ([]string, error) {
	pools, err := c.storagePools(ctx)
	if err != nil {
		return nil, err
	}
	req := recoverPost{Pools: pools}
	var found recoverResult
	if err := c.post(ctx, "/internal/recover/validate", req, &found); err != nil {
		return nil, err
	}
	project, err := c.project(ctx)
	if err != nil {
		return nil, err
	}
	var ours, others []string
	for _, v := range found.UnknownVolumes {
		if v.Type == "container" && v.Project == project && slices.Contains(names, v.Name) {
			ours = append(ours, v.Name)
		} else {
			others = append(others, fmt.Sprintf("%s %s in pool %s", v.Type, v.Name, v.Pool))
		}
	}
	switch {
	case len(ours) == 0:
		return nil, nil
	case len(others) > 0:
		return nil, fmt.Errorf("Incus has lost track of %s, and also of %s: recover them with `incus admin recover`, or delete them: %w",
			strings.Join(ours, ", "), strings.Join(others, ", "), ErrCantRecover)
	case len(found.DependencyErrors) > 0:
		return nil, fmt.Errorf("Incus can't take back %s: %s: %w", strings.Join(ours, ", "), strings.Join(found.DependencyErrors, "; "), ErrCantRecover)
	}
	if err := c.post(ctx, "/internal/recover/import", req, nil); err != nil {
		return nil, err
	}
	return ours, nil
}

// project is the Incus project instances are made in: the connection's, or
// the default one for the incus command, which AgentBox doesn't point at
// another.
func (c Client) project(ctx context.Context) (string, error) {
	if c.cli() {
		return "default", nil
	}
	project := ""
	err := c.do(ctx, []string{"project", "get-current"}, func(s incusclient.InstanceServer) error {
		info, err := s.GetConnectionInfo()
		if err == nil {
			project = info.Project
		}
		return err
	})
	return project, err
}

// storagePools is every storage pool, as recovering is asked for them.
func (c Client) storagePools(ctx context.Context) ([]api.StoragePoolsPost, error) {
	const path = "/1.0/storage-pools?recursion=1"
	var pools []api.StoragePool
	var err error
	if c.cli() {
		err = c.query(ctx, path, &pools, "storage pools")
	} else {
		err = c.do(ctx, []string{"query", path}, func(s incusclient.InstanceServer) (err error) {
			pools, err = s.GetStoragePools()
			return err
		})
	}
	if err != nil {
		return nil, err
	}
	posts := make([]api.StoragePoolsPost, len(pools))
	for i, p := range pools {
		posts[i] = api.StoragePoolsPost{Name: p.Name, Driver: p.Driver, StoragePoolPut: api.StoragePoolPut{Config: p.Config}}
	}
	return posts, nil
}

// post sends body to one of Incus' endpoints and decodes its answer into out,
// unless out is nil: `incus query -X POST --data body path`.
func (c Client) post(ctx context.Context, path string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	args := []string{"query", "-X", "POST", "--data", string(data), path}
	if c.cli() {
		got, err := c.run(ctx, args...)
		if err != nil || out == nil {
			return err
		}
		if err := json.Unmarshal([]byte(got), out); err != nil {
			return fmt.Errorf("parsing what %s answered: %w", path, err)
		}
		return nil
	}
	return c.do(ctx, []string{"query", "-X", "POST", path}, func(s incusclient.InstanceServer) error {
		resp, _, err := s.RawQuery("POST", path, body, "")
		if err != nil || out == nil {
			return err
		}
		return resp.MetadataAsStruct(out)
	})
}
