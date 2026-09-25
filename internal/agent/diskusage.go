package agent

import (
	"context"
	"os"
	"path/filepath"
	"sort"

	"agentbox/internal/image"
)

// pool is the storage pool every agent's machine and every saved base lives
// on; nothing here names another one (Usage.PoolSpace doesn't either).
const pool = "default"

// DiskUsageItem is one thing measured: an agent's machine, a project's base,
// a project's media, and so on.
type DiskUsageItem struct {
	Label string
	Bytes int64
}

// DiskUsageCategory groups items of one kind, largest first, with its own
// total.
type DiskUsageCategory struct {
	Label string
	Bytes int64
	Items []DiskUsageItem
}

type DiskUsage struct {
	Total      int64
	Categories []DiskUsageCategory
}

// DiskUsage measures what AgentBox uses on disk: every agent's machine, the
// base image and each project's saved bases (their Incus volumes, which
// works whether or not the instance is running), and worktrees, media,
// state.db and the daemon log from the host filesystem. It is meant to be
// called when asked, not kept warm on a poll: a full pass walks every
// worktree and every media directory on the host.
func (m *Manager) DiskUsage(ctx context.Context) (DiskUsage, error) {
	projects, err := m.Store.Projects(ctx)
	if err != nil {
		return DiskUsage{}, err
	}
	agents, err := m.Store.Agents(ctx, "")
	if err != nil {
		return DiskUsage{}, err
	}

	machines := DiskUsageCategory{Label: "Agent machines"}
	for _, a := range agents {
		if used, err := m.Incus.VolumeUsage(ctx, pool, a.Instance); err == nil {
			machines.Items = append(machines.Items, DiskUsageItem{Label: a.Ref(), Bytes: used})
		}
	}

	bases := DiskUsageCategory{Label: "Base images and saved bases"}
	if used, err := m.Incus.VolumeUsage(ctx, pool, image.Base); err == nil {
		bases.Items = append(bases.Items, DiskUsageItem{Label: "Base image", Bytes: used})
	}
	for _, p := range projects {
		if used, err := m.Incus.VolumeUsage(ctx, pool, ProjectBaseInstance(p.Name)); err == nil {
			bases.Items = append(bases.Items, DiskUsageItem{Label: p.Name + " base", Bytes: used})
		}
		if used, err := m.Incus.VolumeUsage(ctx, pool, PreviousBaseInstance(p.Name)); err == nil {
			bases.Items = append(bases.Items, DiskUsageItem{Label: p.Name + " base (previous)", Bytes: used})
		}
	}

	worktrees := DiskUsageCategory{Label: "Worktrees"}
	for _, a := range agents {
		if a.Worktree == "" {
			continue
		}
		if size, err := dirSize(a.Worktree); err == nil {
			worktrees.Items = append(worktrees.Items, DiskUsageItem{Label: a.Ref(), Bytes: size})
		}
	}

	media := DiskUsageCategory{Label: "Media"}
	for _, p := range projects {
		if size, err := dirSize(filepath.Join(m.Paths.Data, "media", p.Name)); err == nil {
			media.Items = append(media.Items, DiskUsageItem{Label: p.Name, Bytes: size})
		}
	}

	state := DiskUsageCategory{Label: "state.db and logs"}
	if info, err := os.Stat(m.Paths.StateDB()); err == nil {
		state.Items = append(state.Items, DiskUsageItem{Label: "state.db", Bytes: info.Size()})
	}
	if info, err := os.Stat(m.Paths.DaemonLog()); err == nil {
		state.Items = append(state.Items, DiskUsageItem{Label: "daemon.log", Bytes: info.Size()})
	}

	categories := []DiskUsageCategory{machines, bases, worktrees, media, state}
	var total int64
	for i := range categories {
		sortItemsDesc(categories[i].Items)
		for _, it := range categories[i].Items {
			categories[i].Bytes += it.Bytes
		}
		total += categories[i].Bytes
	}
	sort.SliceStable(categories, func(i, j int) bool { return categories[i].Bytes > categories[j].Bytes })

	return DiskUsage{Total: total, Categories: categories}, nil
}

func sortItemsDesc(items []DiskUsageItem) {
	sort.SliceStable(items, func(i, j int) bool { return items[i].Bytes > items[j].Bytes })
}
