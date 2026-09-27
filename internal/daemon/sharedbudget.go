package daemon

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"agentbox/internal/agent"
	"agentbox/internal/api"
	"agentbox/internal/hostsetup"
	"agentbox/internal/state"
)

// The shared agent budget (agent.SharedBudget): every agent's machine under
// one parent cgroup, with one memory, swap and CPU budget between them.

// sharedBudgetInterval is how often the daemon writes the budget again while
// it's on. Writing is a no-op when nothing changed; what does change on its
// own is memory.high, which is lifted to memory.max while the budget's swap is
// nearly full (agent.ApplyBudget), and the cgroup itself, which a reboot
// makes afresh with no budget in it.
const sharedBudgetInterval = 10 * time.Second

// watchSharedBudget keeps the budget applied while it's on. The first pass
// also puts every agent's raw.lxc where the setting says, for agents made by
// a daemon from before it was turned on.
func (s *Server) watchSharedBudget(ctx context.Context) {
	m := s.manager(nil)
	if on, _, err := m.SharedBudget(ctx); err == nil && on {
		if _, err := m.ApplySharedBudget(ctx); err != nil {
			s.logf("shared budget: %v", err)
		}
	}
	ticker := time.NewTicker(sharedBudgetInterval)
	defer ticker.Stop()
	var last string
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		on, b, err := m.SharedBudget(ctx)
		if err != nil || !on {
			last = ""
			continue
		}
		msg := ""
		if err := agent.ApplyBudget(true, b); err != nil {
			msg = err.Error()
		}
		// Said once, not every ten seconds.
		if msg != "" && msg != last {
			s.logf("shared budget: %s", msg)
		}
		last = msg
	}
}

// sharedBudget is the budget's state, for Settings.
func (s *Server) sharedBudget(ctx context.Context) (api.SharedBudget, error) {
	m := s.manager(nil)
	on, b, err := m.SharedBudget(ctx)
	if err != nil {
		return api.SharedBudget{}, err
	}
	host := agent.ReadHostResources()
	suggested, why := agent.SuggestBudget(host)
	chosen := false
	for _, key := range []string{state.SettingSharedBudgetMemory, state.SettingSharedBudgetSwap, state.SettingSharedBudgetCPU} {
		v, err := s.store.Setting(ctx, key)
		if err != nil {
			return api.SharedBudget{}, err
		}
		chosen = chosen || v != ""
	}
	out := api.SharedBudget{
		On: on, Memory: b.Memory, Swap: b.Swap, CPU: b.CPU, Chosen: chosen,
		Suggested:    api.SharedBudgetSize{Memory: suggested.Memory, Swap: suggested.Swap, CPU: suggested.CPU},
		Why:          why,
		HostSwap:     host.Swap,
		HostSwapKind: host.SwapKind,
		Unsupported:  agent.BudgetSupport(),
		SetupCommand: hostsetup.BudgetCommand,
	}
	if out.Unsupported != "" {
		return out, nil
	}
	if err := agent.BudgetReady(); err != nil {
		out.NotReady = budgetNotReady(err)
		if on {
			out.Problem = "The budget is on, but not applied: " + out.NotReady
		}
	}
	out.Inside, out.Pending = s.budgetMembers(ctx, on)
	return out, nil
}

// budgetNotReady says what BudgetReady found missing, and what fixes it.
func budgetNotReady(err error) string {
	return "it needs " + hostsetup.BudgetCgroupDir + ", a cgroup only root can make: " +
		"Set up asks for your password once, and installs a small unit that makes it at every boot. (" + strings.TrimPrefix(err.Error(), agent.ErrBudgetNotReady.Error()+": ") + ")"
}

// budgetMembers counts the running agents inside the budget, and those that
// aren't where the setting puts them until they restart.
func (s *Server) budgetMembers(ctx context.Context, on bool) (inside, pending int) {
	agents, err := s.store.Agents(ctx, "")
	if err != nil {
		return 0, 0
	}
	for _, a := range agents {
		if a.IsLead() || a.Status != state.AgentReady {
			continue
		}
		in := agent.InBudget(a.Instance)
		if in {
			inside++
		}
		running := in || agent.RunningOutsideBudget(a.Instance)
		if running && in != on {
			pending++
		}
	}
	return inside, pending
}

// updateSharedBudget applies a Settings request's shared budget fields: the
// size first, checked against this host, then the switch, and then brings
// the cgroup and every agent in line.
func (s *Server) updateSharedBudget(ctx context.Context, req api.UpdateSettingsRequest) error {
	if req.SharedBudget == nil && req.SharedBudgetMemory == nil && req.SharedBudgetSwap == nil && req.SharedBudgetCPU == nil {
		return nil
	}
	if why := agent.BudgetSupport(); why != "" {
		return errors.New(why)
	}
	m := s.manager(nil)
	wasOn, b, err := m.SharedBudget(ctx)
	if err != nil {
		return err
	}
	suggested, _ := agent.SuggestBudget(agent.ReadHostResources())
	var writes [][2]string
	if req.SharedBudgetMemory != nil {
		v := strings.TrimSpace(*req.SharedBudgetMemory)
		writes = append(writes, [2]string{state.SettingSharedBudgetMemory, v})
		b.Memory = v
		if v == "" {
			b.Memory = suggested.Memory
		}
	}
	if req.SharedBudgetSwap != nil {
		v := strings.TrimSpace(*req.SharedBudgetSwap)
		writes = append(writes, [2]string{state.SettingSharedBudgetSwap, v})
		b.Swap = v
		if v == "" {
			b.Swap = suggested.Swap
		}
	}
	if req.SharedBudgetCPU != nil {
		n := *req.SharedBudgetCPU
		v := ""
		if n > 0 {
			v = strconv.Itoa(n)
		}
		writes = append(writes, [2]string{state.SettingSharedBudgetCPU, v})
		b.CPU = n
		if n <= 0 {
			b.CPU = suggested.CPU
		}
	}
	if err := b.Validate(agent.ReadHostResources()); err != nil {
		return err
	}
	on := wasOn
	if req.SharedBudget != nil {
		on = *req.SharedBudget
	}
	if on {
		if err := agent.BudgetReady(); err != nil {
			return errors.New("the shared budget can't be turned on yet: " + budgetNotReady(err))
		}
	}
	for _, w := range writes {
		if err := s.store.SetSetting(ctx, w[0], w[1]); err != nil {
			return err
		}
	}
	if req.SharedBudget != nil {
		if err := s.store.SetFlag(ctx, state.SettingSharedBudget, on); err != nil {
			return err
		}
	}
	if !on && !wasOn {
		return nil // resized while off: nothing to apply
	}
	pending, err := m.ApplySharedBudget(ctx)
	if err != nil {
		return err
	}
	if req.SharedBudget != nil && on != wasOn {
		verb := "on"
		if !on {
			verb = "off"
		}
		s.logf("shared budget %s: %s; %d running agent(s) move when they restart", verb, b.Describe(), pending)
	}
	return nil
}
