package memory_test

import (
	"context"
	"testing"
	"time"

	"agentbox/internal/memory"
)

// This file is the harness D72 asks for: a fixture corpus with hand-written
// answers, and a test that scores Search against it so a future change to
// ranking has something to fail against besides "it still compiles". It
// scores memory.Store.SearchBM25 — bm25 alone, exported for tests by
// export_test.go — against the reranked Search over the same rows, which is
// the only way to say plainly whether reranking earned its keep.
//
// The corpus is "harborlink", an imaginary freight-tracking service, built
// the way a real project's memory actually fills up: a run of standing
// facts, decisions and discoveries at various ages, a couple of open issues,
// one superseded memory and one resolved one (so the harness also proves
// Live still holds under reranking), a page of raw events and a page of
// agent reports. Ages are relative to time.Now() so the fixture reads the
// same a year from now as it does today.
const harborlink = "harborlink"

func daysAgo(n int) time.Time { return time.Now().Add(-time.Duration(n) * 24 * time.Hour) }

// harborlinkFixture is the ids searchEvalCases needs to name a right answer.
// Not every memory seeded below is named here: some exist only to give the
// index something else to consider, the way a real project's memory is full
// of rows no particular query is about.
type harborlinkFixture struct {
	port8443, eventsFirst, rateLimitNow           string
	customsTimeout, customsSandboxNow             string
	trackingChecksum, redisEviction, armRunnerOOM string
	webhookRetryPolicy, k8sCurrent                string
	metricsFootnote, metricsRunbook               string

	testFailedChecksum, testFailedCustoms, prMergedRatelimit string
	agentCreatedArm, incidentRedis                           string

	reportChecksum, reportArm, reportCustoms, reportRedis string
}

func seedHarborlink(t *testing.T, s *memory.Store) harborlinkFixture {
	t.Helper()
	ctx := context.Background()
	var f harborlinkFixture

	must := func(m memory.Memory, err error) memory.Memory {
		t.Helper()
		if err != nil {
			t.Fatalf("seeding %q: %v", m.Title, err)
		}
		return m
	}

	// Long-lived facts, none of which decay: true until the project changes.
	f.port8443 = must(s.AddMemory(ctx, memory.Memory{
		Project: harborlink, Kind: memory.KindProject, Importance: 5,
		Title:     "harborlink's API listens on port 8443",
		Content:   "internal/gateway/server.go binds :8443 behind TLS; the plain :8080 listener was removed in the 2.0 rewrite.",
		CreatedAt: daysAgo(200),
	})).ID
	must(s.AddMemory(ctx, memory.Memory{
		Project: harborlink, Kind: memory.KindProject, Importance: 5,
		Title:     "Postgres is the system of record",
		Content:   "internal/store/postgres.go is the only writer; redis is a cache, never authoritative.",
		CreatedAt: daysAgo(190),
	}))
	f.eventsFirst = must(s.AddMemory(ctx, memory.Memory{
		Project: harborlink, Kind: memory.KindProject, Importance: 4,
		Title:     "Every shipment event is appended to the events table before anything else touches it",
		Content:   "internal/tracking/ingest.go writes raw carrier webhooks to the events table first; nothing downstream can lose one even if parsing fails later.",
		CreatedAt: daysAgo(250),
	})).ID
	f.rateLimitNow = must(s.AddMemory(ctx, memory.Memory{
		Project: harborlink, Kind: memory.KindProject, Importance: 3,
		Title:     "Rate limit is enforced per API key at 100 req/s",
		Content:   "internal/gateway/ratelimit.go keys the token bucket by API key since 2.1.",
		CreatedAt: daysAgo(80),
	})).ID

	// A decision, and an episodic anecdote about the same topic, so a query
	// for it can ask which the reader actually wants.
	f.webhookRetryPolicy = must(s.AddMemory(ctx, memory.Memory{
		Project: harborlink, Kind: memory.KindDecision, Importance: 3,
		Title:     "Webhook retries cap at five attempts",
		Content:   "internal/webhook/retry.go stops retrying after 5 attempts and marks the delivery failed.",
		CreatedAt: daysAgo(60),
	})).ID
	must(s.AddMemory(ctx, memory.Memory{
		Project: harborlink, Kind: memory.KindEpisodic, Importance: 3,
		Title:     "A partner complained about missed webhook retries",
		Content:   "Turned out their endpoint was down for six attempts' worth of time; nothing to fix on our side.",
		CreatedAt: daysAgo(58),
	}))

	// Discoveries: things found out the hard way.
	must(s.AddMemory(ctx, memory.Memory{
		Project: harborlink, Kind: memory.KindDiscovery, Importance: 4,
		Title:     "Rate limiting used one shared token bucket for every API key",
		Content:   "internal/gateway/ratelimit.go's bucket wasn't keyed at all before 2.1: one noisy customer could starve every other one.",
		CreatedAt: daysAgo(82),
	}))
	f.customsTimeout = must(s.AddMemory(ctx, memory.Memory{
		Project: harborlink, Kind: memory.KindDiscovery, Importance: 3,
		Title:     "The customs API has a two second timeout",
		Content:   "internal/customs/client.go's default timeout of 2s is too low when DHL's sandbox is slow; raising it to 10s fixed flaky tests.",
		CreatedAt: daysAgo(45),
	})).ID

	// A recency tie-break pair: both mention "customs sandbox", one is a
	// stale anecdote from before the fix above, the other is current.
	must(s.AddMemory(ctx, memory.Memory{
		Project: harborlink, Kind: memory.KindEpisodic, Importance: 2,
		Title:     "The staging customs sandbox was flaky for a week",
		Content:   "Before the 2s timeout was raised, staging customs sandbox calls timed out constantly.",
		CreatedAt: daysAgo(200),
	}))
	f.customsSandboxNow = must(s.AddMemory(ctx, memory.Memory{
		Project: harborlink, Kind: memory.KindDiscovery, Importance: 3,
		Title:     "The customs sandbox now enforces a rate limit of its own",
		Content:   "DHL added a 5 requests/second cap to their sandbox in September; our client already backs off correctly.",
		CreatedAt: daysAgo(3),
	})).ID

	// Open issues, recent, importance-worthy.
	f.trackingChecksum = must(s.AddMemory(ctx, memory.Memory{
		Project: harborlink, Kind: memory.KindIssue, Importance: 4,
		Title:     "Tracking numbers with a checksum mismatch are silently dropped",
		Content:   "internal/tracking/parse.go swallows a checksum mismatch instead of logging it; DHL's numbers hit this weekly.",
		CreatedAt: daysAgo(10),
	})).ID
	f.redisEviction = must(s.AddMemory(ctx, memory.Memory{
		Project: harborlink, Kind: memory.KindIssue, Importance: 3,
		Title:     "Redis eviction drops session state under memory pressure",
		Content:   "A traffic spike hit maxmemory and redis evicted session keys, logging users out mid-session.",
		CreatedAt: daysAgo(5),
	})).ID
	f.armRunnerOOM = must(s.AddMemory(ctx, memory.Memory{
		Project: harborlink, Kind: memory.KindIssue, Importance: 3,
		Title:     "An ARM runner hits OOM during container scans",
		Content:   "The scan process is killed near the end of a large image; smaller base images avoid it for now.",
		CreatedAt: daysAgo(8),
	})).ID

	// An importance tie-break pair: same age, same kind, one is a footnote
	// and one is the thing on-call actually cares about. bm25 favours the
	// footnote — both its terms are in the title, where the other memory has
	// only one — so this is also the case that shows reranking's limit: a
	// weight of 0.10 on importance isn't enough to undo a real bm25 gap.
	f.metricsFootnote = must(s.AddMemory(ctx, memory.Memory{
		Project: harborlink, Kind: memory.KindProject, Importance: 2,
		Title:     "Metrics are scraped by Prometheus every 15s",
		Content:   "internal/metrics/registry.go exposes /metrics; largely undocumented.",
		CreatedAt: daysAgo(100),
	})).ID
	f.metricsRunbook = must(s.AddMemory(ctx, memory.Memory{
		Project: harborlink, Kind: memory.KindProject, Importance: 5,
		Title:     "Metrics gaps longer than 2 minutes page on-call",
		Content:   "internal/metrics/alerts.go pages when Prometheus can't scrape for 2 minutes straight; this is the top on-call runbook entry.",
		CreatedAt: daysAgo(95),
	})).ID

	// A bm25-dominance case: a recent, low-importance, episodic mention of
	// the same distinctive token (8443) as the standing fact above. Search
	// must not let recency promote the anecdote over the actual fact.
	must(s.AddMemory(ctx, memory.Memory{
		Project: harborlink, Kind: memory.KindEpisodic, Importance: 2,
		Title:     "Load test throughput dipped for a few minutes",
		Content:   "A script hammering port 8443 during the day turned out to be an unrelated firewall rule, not the gateway.",
		CreatedAt: daysAgo(3),
	}))

	// A superseded memory and a resolved one: both must vanish from Search
	// under reranking exactly as they did under bm25 alone, because `live`
	// filters them out before either ranking ever runs.
	oldK8s := must(s.AddMemory(ctx, memory.Memory{
		Project: harborlink, Kind: memory.KindProject, Importance: 3,
		Title: "harborlink runs on Kubernetes 1.27", CreatedAt: daysAgo(300),
	}))
	f.k8sCurrent = must(s.AddMemory(ctx, memory.Memory{
		Project: harborlink, Kind: memory.KindProject, Importance: 3,
		Title: "harborlink runs on Kubernetes 1.29", Content: "Upgraded from 1.27 in the spring.",
		CreatedAt: daysAgo(20), SupersedesID: oldK8s.ID,
	})).ID
	resolvedDup := must(s.AddMemory(ctx, memory.Memory{
		Project: harborlink, Kind: memory.KindIssue, Importance: 4,
		Title: "Webhook retries could duplicate a delivery", CreatedAt: daysAgo(90),
	}))
	if _, err := s.ResolveMemory(ctx, harborlink, resolvedDup.ID, "fixed by idempotency keys in 2.1"); err != nil {
		t.Fatalf("resolving fixture memory: %v", err)
	}

	addEvent := func(typ, payload string, age int) string {
		e, err := s.AppendEvent(ctx, memory.Event{
			Project: harborlink, Type: typ, Payload: []byte(payload), At: daysAgo(age),
		})
		if err != nil {
			t.Fatalf("seeding event %q: %v", typ, err)
		}
		return e.ID
	}
	f.testFailedCustoms = addEvent("test_failed", `{"error":"customs API timeout calling the DHL sandbox"}`, 46)
	f.testFailedChecksum = addEvent("test_failed", `{"error":"checksum mismatch on tracking number 1Z999AA10123456784"}`, 9)
	f.prMergedRatelimit = addEvent("pr_merged", `{"title":"Key the rate limit bucket per API key","branch":"fix/ratelimit-keying"}`, 81)
	f.agentCreatedArm = addEvent("agent_created", `{"agent":"agent-12","task":"Investigate ARM runner OOM in a container scan"}`, 9)
	f.incidentRedis = addEvent("incident", `{"summary":"redis evicted session state during a traffic spike, users were logged out"}`, 4)
	addEvent("deploy", `{"version":"2.1.0","notes":"idempotency keys for webhook delivery"}`, 89)
	addEvent("deploy", `{"version":"2.2.0","notes":"kubernetes 1.29 upgrade"}`, 20)
	addEvent("question_asked", `{"question":"should tracking search support partial checksum matches"}`, 15)
	addEvent("notes_changed", `{"summary":"Q3 goal: cut customs API p95 latency"}`, 25)

	addReport := func(agent, task, status, summary string, age int, extra ...func(*memory.Report)) string {
		r := memory.Report{Project: harborlink, Agent: agent, Task: task, Status: status, Summary: summary, CreatedAt: daysAgo(age)}
		for _, e := range extra {
			e(&r)
		}
		added, err := s.AddReport(ctx, r)
		if err != nil {
			t.Fatalf("seeding report %q: %v", task, err)
		}
		return added.ID
	}
	f.reportCustoms = addReport("agent-03", "Fix the customs API timeout", memory.StatusDone,
		"Raised internal/customs/client.go's timeout to 10s; the DHL sandbox flake stopped.", 44)
	f.reportChecksum = addReport("agent-11", "Investigate dropped tracking numbers", memory.StatusPartial,
		"Confirmed internal/tracking/parse.go swallows a checksum mismatch instead of logging it. Have not fixed it yet.", 9,
		func(r *memory.Report) {
			r.RemainingIssues = []string{"Log and surface the checksum failure instead of dropping it"}
		})
	f.reportArm = addReport("agent-07", "Investigate ARM runner container scan hangs", memory.StatusBlocked,
		"Reproduced the hang on ARM; the scan process gets OOM-killed near the end of a large image.", 8,
		func(r *memory.Report) {
			r.RemainingIssues = []string{"Needs more memory on ARM runners or a smaller base image"}
		})
	addReport("agent-09", "Key the rate limiter per API key", memory.StatusDone,
		"internal/gateway/ratelimit.go now buckets by API key; one noisy customer can no longer starve the rest.", 81)
	addReport("agent-14", "Upgrade to Kubernetes 1.29", memory.StatusDone, "Rolling upgrade completed with no downtime.", 20)
	addReport("agent-02", "Add idempotency keys to webhook delivery", memory.StatusDone,
		"internal/webhook/retry.go now sends an idempotency key so a retried delivery can't duplicate.", 90)
	f.reportRedis = addReport("agent-16", "Investigate Redis eviction during traffic spikes", memory.StatusPartial,
		"redis's maxmemory-policy evicted session keys under a spike; recommending a dedicated session store.", 4)
	addReport("agent-05", "Demo tracking search for the board", memory.StatusDone,
		"Live demo went fine; the board asked about partial checksum matches.", 15)

	return f
}

// searchEvalCase is one hand-labelled query: the rows Search should surface
// for it, per index. An empty slice means the query isn't about that index
// at all, and it isn't scored there.
type searchEvalCase struct {
	name     string
	query    string
	memories []string
	events   []string
	reports  []string
}

func searchEvalCases(f harborlinkFixture) []searchEvalCase {
	return []searchEvalCase{
		{
			name:     "exact port number, dominance over a recent low-value mention",
			query:    "8443",
			memories: []string{f.port8443},
		},
		{
			name:     "exact file path",
			query:    "internal/tracking/parse.go",
			memories: []string{f.trackingChecksum},
			reports:  []string{f.reportChecksum},
		},
		{
			name:     "checksum mismatch, recent open issue over an older discovery",
			query:    "checksum mismatch tracking number",
			memories: []string{f.trackingChecksum},
			events:   []string{f.testFailedChecksum},
			reports:  []string{f.reportChecksum},
		},
		{
			name:     "customs API timeout",
			query:    "customs API timeout",
			memories: []string{f.customsTimeout},
			events:   []string{f.testFailedCustoms},
			reports:  []string{f.reportCustoms},
		},
		{
			name:     "recency tie-break: current sandbox behaviour over a stale anecdote",
			query:    "customs sandbox",
			memories: []string{f.customsSandboxNow},
		},
		{
			name:     "kind tie-break: the decision over the anecdote about it",
			query:    "webhook retries attempts",
			memories: []string{f.webhookRetryPolicy},
		},
		{
			name:     "importance tie-break: the runbook entry over the footnote",
			query:    "Prometheus metrics",
			memories: []string{f.metricsRunbook},
		},
		{
			name:     "rate limit per API key, current state over the historic bug",
			query:    "rate limit per API key",
			memories: []string{f.rateLimitNow},
			events:   []string{f.prMergedRatelimit},
		},
		{
			name:     "open issue, recent",
			query:    "ARM runner container scan OOM",
			memories: []string{f.armRunnerOOM},
			events:   []string{f.agentCreatedArm},
			reports:  []string{f.reportArm},
		},
		{
			name:     "redis session eviction",
			query:    "redis session eviction memory pressure",
			memories: []string{f.redisEviction},
			events:   []string{f.incidentRedis},
			reports:  []string{f.reportRedis},
		},
		{
			name:     "superseded memory must not resurface",
			query:    "harborlink runs on Kubernetes",
			memories: []string{f.k8sCurrent},
		},
		{
			name:     "durable fact survives despite its age",
			query:    "events table shipment webhooks",
			memories: []string{f.eventsFirst},
		},
		{
			name:     "a natural-language question, answered through the loose fallback",
			query:    "why do tracking numbers get dropped",
			memories: []string{f.trackingChecksum},
		},
	}
}

// Evaluation. Recall at k and mean reciprocal rank are the two standard,
// defensible measures for "did the right row come back" (precision alone
// isn't, since it rewards returning nothing over returning a short wrong
// list). Both are used here rather than picking one: most of these queries
// have a single best answer, which is what MRR is for, but a few name more
// than one relevant row, which only recall can credit.
const (
	// evalLimit is how many rows each search is asked for. It is the pool
	// MRR and recall are computed against, not the practical size a caller
	// would actually request — big enough that a row reranking pushed down
	// rather than out is still visible in the number.
	evalLimit = 10
	// evalRecallK is the cutoff recall is measured at: could the row that
	// mattered have been read without scrolling. It matches contextSearchHits
	// in context.go, the number of hits the context builder actually keeps
	// per index.
	evalRecallK = 5

	// rerankMRRFloor is the reranked MRR over the harborlink fixtures at the
	// commit that added this harness: 0.978, against 0.957 for bm25 alone
	// (see the PR body for the full breakdown). The margin below it is for
	// the recency term's continuous drift with the clock, not for slack to
	// regress into — a change to the weights, the pool size or the fixtures
	// that drops below this line has made ranking worse against hand-labelled
	// answers, not just different, and the floor should only move up.
	rerankMRRFloor = 0.95
)

// evalScore accumulates reciprocal rank and recall@evalRecallK over every
// list a case actually named an answer for. A case's memories, events and
// reports are three separate observations, since bm25 (and now reranking)
// treats them as three separate lists.
type evalScore struct {
	rrSum, recallSum float64
	n                int
}

func (e *evalScore) add(rr, recall float64) {
	e.rrSum += rr
	e.recallSum += recall
	e.n++
}

func (e evalScore) mean() (mrr, recall float64) {
	if e.n == 0 {
		return 0, 0
	}
	return e.rrSum / float64(e.n), e.recallSum / float64(e.n)
}

// reciprocalRank is 1/(position of the first relevant id), or 0 if none of
// them came back at all.
func reciprocalRank(got, relevant []string) float64 {
	want := toSet(relevant)
	for i, id := range got {
		if want[id] {
			return 1 / float64(i+1)
		}
	}
	return 0
}

// recallAt is the fraction of relevant ids present in got's first k.
func recallAt(got, relevant []string, k int) float64 {
	if k < len(got) {
		got = got[:k]
	}
	present := toSet(got)
	hits := 0
	for _, id := range relevant {
		if present[id] {
			hits++
		}
	}
	return float64(hits) / float64(len(relevant))
}

func toSet(ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = true
	}
	return out
}

func idsOf[T any](rows []T, id func(T) string) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = id(row)
	}
	return out
}

// listCheck is one scored observation: one case's expectation for one index.
type listCheck struct {
	label         string
	got, relevant []string
}

func checksFor(c searchEvalCase, r memory.Results) []listCheck {
	var out []listCheck
	if len(c.memories) > 0 {
		out = append(out, listCheck{"memories", idsOf(r.Memories, func(m memory.Memory) string { return m.ID }), c.memories})
	}
	if len(c.events) > 0 {
		out = append(out, listCheck{"events", idsOf(r.Events, func(e memory.Event) string { return e.ID }), c.events})
	}
	if len(c.reports) > 0 {
		out = append(out, listCheck{"reports", idsOf(r.Reports, func(rp memory.Report) string { return rp.ID }), c.reports})
	}
	return out
}

// TestSearchQualityAgainstFixtures is the harness D72 asks for: it scores
// bm25 alone and the reranked Search against the same hand-labelled
// fixtures, logs both so a PR body can quote them, and fails if the reranked
// number drops below the recorded floor. Run `go test ./internal/memory/...
// -run TestSearchQualityAgainstFixtures -v` to see the per-case breakdown.
func TestSearchQualityAgainstFixtures(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	f := seedHarborlink(t, s)
	cases := searchEvalCases(f)

	var bm25Score, rerankedScore evalScore
	for _, c := range cases {
		bm25Results, err := s.SearchBM25(ctx, harborlink, c.query, evalLimit)
		if err != nil {
			t.Fatalf("%s: bm25 search: %v", c.name, err)
		}
		rerankedResults, err := s.Search(ctx, harborlink, c.query, evalLimit)
		if err != nil {
			t.Fatalf("%s: search: %v", c.name, err)
		}

		bChecks, rChecks := checksFor(c, bm25Results), checksFor(c, rerankedResults)
		for i := range bChecks {
			bRR, bRecall := reciprocalRank(bChecks[i].got, bChecks[i].relevant), recallAt(bChecks[i].got, bChecks[i].relevant, evalRecallK)
			rRR, rRecall := reciprocalRank(rChecks[i].got, rChecks[i].relevant), recallAt(rChecks[i].got, rChecks[i].relevant, evalRecallK)
			bm25Score.add(bRR, bRecall)
			rerankedScore.add(rRR, rRecall)
			change := "="
			switch {
			case rRR > bRR:
				change = "+"
			case rRR < bRR:
				change = "-"
			}
			t.Logf("%-70s [%-8s] bm25 rr=%.2f recall@%d=%.2f  ->  reranked rr=%.2f recall@%d=%.2f  (%s)",
				c.name, bChecks[i].label, bRR, evalRecallK, bRecall, rRR, evalRecallK, rRecall, change)
		}
	}

	bMRR, bRecall := bm25Score.mean()
	rMRR, rRecall := rerankedScore.mean()
	t.Logf("bm25 only:  MRR@%d = %.3f  recall@%d = %.3f  over %d scored lists", evalLimit, bMRR, evalRecallK, bRecall, bm25Score.n)
	t.Logf("reranked:   MRR@%d = %.3f  recall@%d = %.3f  over %d scored lists", evalLimit, rMRR, evalRecallK, rRecall, rerankedScore.n)

	if rMRR < rerankMRRFloor {
		t.Errorf("reranked MRR@%d = %.3f, want at least %.3f (the recorded floor)", evalLimit, rMRR, rerankMRRFloor)
	}
}
