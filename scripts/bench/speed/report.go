package main

import (
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"sort"
	"strings"
	"time"
)

// stat is a sample's median and spread.
type stat struct {
	N      int     `json:"n"`
	Median float64 `json:"median"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	P25    float64 `json:"p25"`
	P75    float64 `json:"p75"`
}

func summarize(xs []float64) stat {
	if len(xs) == 0 {
		return stat{}
	}
	s := slices.Clone(xs)
	sort.Float64s(s)
	return stat{N: len(s), Median: quantile(s, 0.5), Min: s[0], Max: s[len(s)-1], P25: quantile(s, 0.25), P75: quantile(s, 0.75)}
}

// quantile of a sorted sample, interpolating between its points.
func quantile(s []float64, q float64) float64 {
	pos := q * float64(len(s)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return s[lo] + (s[hi]-s[lo])*(pos-float64(lo))
}

// cell is one task at one load in one mode, over its runs.
type cell struct {
	Task  string `json:"task"`
	Load  int    `json:"load"`
	Mode  string `json:"mode"`
	Runs  int    `json:"runs"`
	OK    int    `json:"ok"`     // runs that did the task
	Fails int    `json:"failed"` // runs that didn't finish (Error); left out of the times

	Wall      stat `json:"wallMs"`
	Setup     stat `json:"setupMs"`
	Startup   stat `json:"startupMs"`
	Model     stat `json:"modelMs"`
	Tool      stat `json:"toolMs"`
	BuildTool stat `json:"buildToolMs"`
	OtherTool stat `json:"otherToolMs"`
	Hook      stat `json:"hookMs"`
	Tail      stat `json:"tailMs"`
	API       stat `json:"apiMs"`
	Check     stat `json:"checkMs"`
	Turns     stat `json:"turns"`
	ToolCalls stat `json:"toolCalls"`
	// PerTurn is the model time of one model call, and Prompt the tokens it
	// read: a bigger prompt (a brief) shows here.
	PerTurn stat `json:"modelMsPerTurn"`
	Prompt  stat `json:"promptTokensPerTurn"`
	Output  stat `json:"outputTokens"`

	// Slowdown is the median wall time over the host's at the same task and
	// load; 0 when there is no host cell.
	Slowdown float64 `json:"slowdown,omitempty"`

	// What the runs' builds saw, every distinct value.
	CPUs       []string `json:"cpus"`
	GOMAXPROCS []string `json:"gomaxprocs"`
	GOFLAGS    []string `json:"goflags"`
	Loadavg    []string `json:"loadavg"`
	Models     []string `json:"models"`
	Versions   []string `json:"claudeVersions"`
	GoVersions []string `json:"goVersions"`
	// Backgrounded is how many commands, over all the runs, went on in the
	// background: past the Bash tool's timeout, the model waits on them.
	Backgrounded int `json:"backgrounded"`
	// Tools is each tool's calls and time per run, on average.
	Tools map[string]toolStat `json:"tools"`
}

type probeStat struct {
	Kind  string   `json:"kind"`
	Load  int      `json:"load"`
	Runs  int      `json:"runs"`
	Fails int      `json:"failed"`
	Wall  stat     `json:"wallMs"`
	Vet   stat     `json:"vetMs"`
	Build stat     `json:"buildMs"`
	Test  stat     `json:"testMs"`
	CPUs  []string `json:"cpus"`
	Flags []string `json:"goflags"`
}

type summary struct {
	Generated time.Time   `json:"generated"`
	Commit    string      `json:"fixtureCommit"`
	Config    config      `json:"config"`
	Cells     []cell      `json:"cells"`
	Probes    []probeStat `json:"probes"`
	Warnings  []string    `json:"warnings,omitempty"`
}

func distinct(xs []string) []string {
	var out []string
	for _, x := range xs {
		if x != "" && !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}

func summarizeAll(cfg config, commit string, records []record, probes []probeRun) summary {
	s := summary{Generated: time.Now().UTC(), Commit: commit, Config: cfg}
	type key struct {
		task string
		load int
		mode string
	}
	groups := map[key][]record{}
	var keys []key
	for _, r := range records {
		k := key{r.Task, r.Load, r.Mode}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], r)
	}
	order := func(xs []string, x string) int {
		if i := slices.Index(xs, x); i >= 0 {
			return i
		}
		return len(xs)
	}
	slices.SortFunc(keys, func(a, b key) int {
		if c := order(allTasks, a.task) - order(allTasks, b.task); c != 0 {
			return c
		}
		if a.load != b.load {
			return a.load - b.load
		}
		return order(allModes, a.mode) - order(allModes, b.mode)
	})
	hostWall := map[key]float64{}
	for _, k := range keys {
		c := cell{Task: k.task, Load: k.load, Mode: k.mode, Tools: map[string]toolStat{}}
		var wall, setup, startup, model, tool, build, other, hook, tail, api, check, turns, calls, perTurn, prompt, output []float64
		var cpus, gomax, flags, loads, models, versions, gos []string
		for _, r := range groups[k] {
			c.Runs++
			if r.OK {
				c.OK++
			}
			if r.Error != "" {
				c.Fails++
				continue
			}
			bd := r.Breakdown
			wall = append(wall, r.WallMS)
			setup = append(setup, r.SetupMS)
			startup = append(startup, r.StartupMS)
			model = append(model, bd.ModelMS)
			tool = append(tool, bd.ToolMS)
			build = append(build, bd.BuildToolMS)
			other = append(other, bd.ToolMS-bd.BuildToolMS)
			hook = append(hook, bd.HookMS)
			tail = append(tail, r.TailMS)
			if bd.APIMS > 0 {
				api = append(api, bd.APIMS)
			}
			check = append(check, r.CheckMS)
			turns = append(turns, float64(bd.Turns))
			calls = append(calls, float64(bd.ToolCalls))
			if bd.Turns > 0 {
				perTurn = append(perTurn, bd.ModelMS/float64(bd.Turns))
				prompt = append(prompt, float64(bd.Tokens.Prompt())/float64(bd.Turns))
			}
			output = append(output, float64(bd.Tokens.Output))
			if r.Env != nil {
				cpus = append(cpus, r.Env.Nproc)
				gomax = append(gomax, r.Env.Env["GOMAXPROCS"])
				flags = append(flags, r.Env.Env["GOFLAGS"])
				loads = append(loads, r.Env.Loadavg)
				gos = append(gos, r.Env.GoVersion)
			}
			models = append(models, bd.Model)
			versions = append(versions, bd.Version)
			c.Backgrounded += bd.Backgrounded
			for name, st := range bd.Tools {
				t := c.Tools[name]
				t.Calls += st.Calls
				t.MS += st.MS
				c.Tools[name] = t
			}
		}
		if n := len(wall); n > 0 {
			for name, t := range c.Tools {
				c.Tools[name] = toolStat{Calls: t.Calls / n, MS: t.MS / float64(n)}
			}
		}
		c.Wall, c.Setup, c.Startup, c.Model = summarize(wall), summarize(setup), summarize(startup), summarize(model)
		c.Tool, c.BuildTool, c.OtherTool, c.Hook = summarize(tool), summarize(build), summarize(other), summarize(hook)
		c.Tail, c.API, c.Check, c.Turns = summarize(tail), summarize(api), summarize(check), summarize(turns)
		c.ToolCalls, c.PerTurn, c.Prompt, c.Output = summarize(calls), summarize(perTurn), summarize(prompt), summarize(output)
		c.CPUs, c.GOMAXPROCS, c.GOFLAGS, c.Loadavg = distinct(cpus), distinct(gomax), distinct(flags), distinct(loads)
		c.Models, c.Versions, c.GoVersions = distinct(models), distinct(versions), distinct(gos)
		if k.mode == modeHost && c.Wall.N > 0 {
			hostWall[key{k.task, k.load, ""}] = c.Wall.Median
		}
		s.Cells = append(s.Cells, c)
	}
	for i, c := range s.Cells {
		if h := hostWall[key{c.Task, c.Load, ""}]; h > 0 && c.Wall.N > 0 {
			s.Cells[i].Slowdown = c.Wall.Median / h
		}
	}

	type pkey struct {
		kind string
		load int
	}
	pgroups := map[pkey][]probeRun{}
	var pkeys []pkey
	for _, p := range probes {
		k := pkey{p.Kind, p.Load}
		if _, ok := pgroups[k]; !ok {
			pkeys = append(pkeys, k)
		}
		pgroups[k] = append(pgroups[k], p)
	}
	slices.SortStableFunc(pkeys, func(a, b pkey) int {
		if a.kind != b.kind {
			return strings.Compare(a.kind, b.kind)
		}
		return a.load - b.load
	})
	for _, k := range pkeys {
		ps := probeStat{Kind: k.kind, Load: k.load}
		var wall, vet, build, test []float64
		var cpus, flags []string
		for _, p := range pgroups[k] {
			ps.Runs++
			if p.Error != "" {
				ps.Fails++
				continue
			}
			wall = append(wall, p.WallMS)
			if p.Check != nil {
				vet = append(vet, p.Check.VetMS)
				build = append(build, p.Check.BuildMS)
				test = append(test, p.Check.TestMS)
			}
			if p.Env != nil {
				cpus = append(cpus, p.Env.Nproc)
				flags = append(flags, p.Env.Env["GOFLAGS"])
			}
		}
		ps.Wall, ps.Vet, ps.Build, ps.Test = summarize(wall), summarize(vet), summarize(build), summarize(test)
		ps.CPUs, ps.Flags = distinct(cpus), distinct(flags)
		s.Probes = append(s.Probes, ps)
	}
	s.Warnings = warnings(s, records)
	return s
}

func warnings(s summary, records []record) []string {
	var w []string
	var models, versions, gos []string
	byMode := map[string][]string{}
	for _, c := range s.Cells {
		models = append(models, c.Models...)
		versions = append(versions, c.Versions...)
		gos = append(gos, c.GoVersions...)
		byMode[c.Mode] = distinct(append(byMode[c.Mode], c.Versions...))
		if c.Backgrounded > 0 {
			w = append(w, fmt.Sprintf("%s at load %d in %s: %d commands went on in the background (a build that outlasts the Bash tool's 2-minute timeout does), and the model spent calls waiting on them.", c.Task, c.Load, c.Mode, c.Backgrounded))
		}
		if c.Runs > 0 && c.OK < c.Runs {
			w = append(w, fmt.Sprintf("%s at load %d in %s: the task was done in %d of %d runs; their times are still in the table.", c.Task, c.Load, c.Mode, c.OK, c.Runs))
		}
	}
	if m := distinct(models); len(m) > 1 {
		w = append(w, "The runs didn't all use the same model: "+strings.Join(m, ", ")+".")
	}
	if len(distinct(versions)) > 1 {
		var vs []string
		for _, mode := range allModes {
			if len(byMode[mode]) > 0 {
				vs = append(vs, mode+" "+strings.Join(byMode[mode], "/"))
			}
		}
		w = append(w, "Claude Code's version differs between modes ("+strings.Join(vs, ", ")+"): part of a difference can be the version's.")
	}
	if g := distinct(gos); len(g) > 1 {
		w = append(w, "The builds didn't all use the same Go ("+strings.Join(g, ", ")+"): part of a build's difference can be Go's.")
	}
	for _, r := range records {
		if r.Error != "" {
			w = append(w, fmt.Sprintf("%s at load %d in %s, run %d, failed and is left out: %s", r.Task, r.Load, r.Mode, r.Run, truncate(r.Error, 300)))
		}
	}
	return w
}

func secs(msv float64) string {
	return fmt.Sprintf("%.1fs", msv/1000)
}

func spread(s stat) string {
	if s.N == 0 {
		return "–"
	}
	if s.N == 1 {
		return secs(s.Median)
	}
	return fmt.Sprintf("%s (%s–%s)", secs(s.Median), secs(s.Min), secs(s.Max))
}

func med(s stat) string {
	if s.N == 0 {
		return "–"
	}
	return secs(s.Median)
}

func count(s stat) string {
	if s.N == 0 {
		return "–"
	}
	return fmt.Sprintf("%.0f", s.Median)
}

func kilo(s stat) string {
	if s.N == 0 {
		return "–"
	}
	return fmt.Sprintf("%.1fk", s.Median/1000)
}

func list(xs []string) string {
	if len(xs) == 0 {
		return "–"
	}
	return strings.Join(xs, ", ")
}

func delta(a, b stat) string {
	if a.N == 0 || b.N == 0 {
		return "–"
	}
	return fmt.Sprintf("%+.1fs", (a.Median-b.Median)/1000)
}

// writeReport writes the summary as Markdown.
func writeReport(w io.Writer, s summary) {
	cfg := s.Config
	fmt.Fprintf(w, "# AgentBox speed benchmark\n\n")
	loads := make([]string, len(cfg.Loads))
	for i, l := range cfg.Loads {
		loads[i] = fmt.Sprint(l)
	}
	effort := cfg.Effort + " effort"
	if cfg.Effort == "" {
		effort = "each one's default effort"
	}
	runs := fmt.Sprintf("%d runs", cfg.Runs)
	if cfg.Runs == 1 {
		runs = "1 run"
	}
	fmt.Fprintf(w, "%s. Fixture commit `%s`, model `%s` at %s, %s per cell, other agents running %s: %s.\n\n",
		s.Generated.Format("2006-01-02 15:04 MST"), short(s.Commit), cfg.Model, effort, runs, cfg.LoadKind, strings.Join(loads, ", "))
	fmt.Fprintf(w, "Modes: **host** is `claude -p` on this computer; **exec** is the same `claude -p` in an agent's machine, through `agentbox exec`; **agent** is an AgentBox agent made through the daemon's API with the prompt as its task, timed from asking for it until its turn ended.\n\n")

	fmt.Fprintf(w, "## Runs\n\n")
	fmt.Fprintf(w, "Medians over the runs that finished; the wall time also gives the fastest and slowest. *Setup* is making the agent until its task was in its chat; *startup* is until Claude Code had the prompt; *model* is the rest of Claude Code's time that wasn't a tool call; *build tools* are the tool calls that built or tested, and *check.sh* the part of them check.sh itself measured (the rest is the tool call around it: hooks, the shell). CPUs are what the builds saw: `nproc`, `GOMAXPROCS`, `GOFLAGS`.\n\n")
	fmt.Fprintf(w, "| task | load | mode | done | wall | vs host | setup | startup | model | build tools | check.sh | other tools | hooks | model calls | s/call | prompt tok/call | CPUs seen |\n")
	fmt.Fprintf(w, "|---|---:|---|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---|\n")
	for _, c := range s.Cells {
		vs := "–"
		if c.Slowdown > 0 {
			vs = fmt.Sprintf("%.2f×", c.Slowdown)
		}
		perCall := "–"
		if c.PerTurn.N > 0 {
			perCall = fmt.Sprintf("%.1f", c.PerTurn.Median/1000)
		}
		cpus := list(c.CPUs) + " · " + list(c.GOMAXPROCS) + " · " + list(c.GOFLAGS)
		setup := "–" // only an agent is made
		if c.Mode == modeAgent {
			setup = med(c.Setup)
		}
		fmt.Fprintf(w, "| %s | %d | %s | %d/%d | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n",
			c.Task, c.Load, c.Mode, c.OK, c.Runs, spread(c.Wall), vs, setup, med(c.Startup), med(c.Model),
			med(c.BuildTool), med(c.Check), med(c.OtherTool), med(c.Hook), count(c.Turns), perCall, kilo(c.Prompt), cpus)
	}

	host := map[string]cell{}
	for _, c := range s.Cells {
		if c.Mode == modeHost {
			host[fmt.Sprint(c.Task, c.Load)] = c
		}
	}
	var rows []string
	for _, c := range s.Cells {
		h, ok := host[fmt.Sprint(c.Task, c.Load)]
		if c.Mode == modeHost || !ok {
			continue
		}
		calls, prompt := "–", "–"
		if c.Turns.N > 0 && h.Turns.N > 0 {
			calls = fmt.Sprintf("%+.0f", c.Turns.Median-h.Turns.Median)
		}
		if c.Prompt.N > 0 && h.Prompt.N > 0 {
			prompt = fmt.Sprintf("%+.1fk", (c.Prompt.Median-h.Prompt.Median)/1000)
		}
		rows = append(rows, fmt.Sprintf("| %s | %d | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |\n", c.Task, c.Load, c.Mode,
			delta(c.Wall, h.Wall), delta(c.Setup, h.Setup), delta(c.Startup, h.Startup), delta(c.Model, h.Model),
			delta(c.BuildTool, h.BuildTool), delta(c.Check, h.Check), delta(c.OtherTool, h.OtherTool), delta(c.Tail, h.Tail), calls, prompt))
	}
	if len(rows) > 0 {
		fmt.Fprintf(w, "\n## Where the extra time goes\n\n")
		fmt.Fprintf(w, "Each mode against the host at the same task and load: the difference of the medians, part by part. They are differences of medians, so they needn't add up to the wall time's exactly.\n\n")
		fmt.Fprintf(w, "| task | load | mode | wall | setup | startup | model | build tools | check.sh | other tools | tail | model calls | prompt tok/call |\n")
		fmt.Fprintf(w, "|---|---:|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|\n")
		for _, r := range rows {
			fmt.Fprint(w, r)
		}
	}

	var bare, rtt []probeStat
	for _, p := range s.Probes {
		if p.Kind == "exec-rtt" {
			rtt = append(rtt, p)
		} else {
			bare = append(bare, p)
		}
	}
	if len(bare) > 0 {
		fmt.Fprintf(w, "\n## The build alone\n\n")
		fmt.Fprintf(w, "`./check.sh` run directly, with no model: a cold build cache, then vet, build and test. This is how much of a difference the machine itself makes to the build task.\n\n")
		fmt.Fprintf(w, "| where | load | runs | check.sh | vet | build | test | CPUs seen |\n|---|---:|---:|---|---:|---:|---:|---|\n")
		for _, p := range bare {
			fmt.Fprintf(w, "| %s | %d | %d | %s | %s | %s | %s | %s · %s |\n", strings.TrimPrefix(p.Kind, "bare-"), p.Load,
				p.Runs-p.Fails, spread(p.Wall), med(p.Vet), med(p.Build), med(p.Test), list(p.CPUs), list(p.Flags))
		}
	}
	if len(rtt) > 0 {
		fmt.Fprintf(w, "\n## Getting a command into the machine\n\n")
		fmt.Fprintf(w, "`agentbox exec <agent> -- true`: what every command the exec mode sends costs before it starts.\n\n")
		fmt.Fprintf(w, "| load | runs | round trip |\n|---:|---:|---|\n")
		for _, p := range rtt {
			fmt.Fprintf(w, "| %d | %d | %s |\n", p.Load, p.Runs-p.Fails, spreadMS(p.Wall))
		}
	}

	fmt.Fprintf(w, "\n## Tools the model called\n\n")
	fmt.Fprintf(w, "Calls and seconds per run, on average, by tool.\n\n")
	fmt.Fprintf(w, "| task | load | mode | tools |\n|---|---:|---|---|\n")
	for _, c := range s.Cells {
		names := slices.Sorted(maps.Keys(c.Tools))
		sort.SliceStable(names, func(i, j int) bool { return c.Tools[names[i]].MS > c.Tools[names[j]].MS })
		var parts []string
		for _, n := range names {
			t := c.Tools[n]
			parts = append(parts, fmt.Sprintf("%s %d× %.1fs", n, t.Calls, t.MS/1000))
		}
		fmt.Fprintf(w, "| %s | %d | %s | %s |\n", c.Task, c.Load, c.Mode, list(parts))
	}
	if len(s.Warnings) > 0 {
		fmt.Fprintf(w, "\n## Warnings\n\n")
		for _, x := range s.Warnings {
			fmt.Fprintf(w, "- %s\n", x)
		}
	}
}

func spreadMS(s stat) string {
	if s.N == 0 {
		return "–"
	}
	return fmt.Sprintf("%.0f ms (%.0f–%.0f)", s.Median, s.Min, s.Max)
}

func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}
