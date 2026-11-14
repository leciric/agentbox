package state

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// seedArchive archives n agents over two projects, a few to each second so
// the order's tie on the time is broken by project and name, with every pull
// request state among them.
func seedArchive(t *testing.T, st *Store, n int) {
	t.Helper()
	ctx := context.Background()
	for _, p := range []string{"pawly", "other"} {
		if err := st.AddProject(ctx, Project{Name: p, Root: "/src/" + p, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Unix(1_800_000_000, 0)
	states := []string{PRMerged, PRMerged, PROpen, PRClosed, ""}
	for i := range n {
		project := []string{"pawly", "other"}[i%2]
		a := Agent{Project: project, Name: fmt.Sprintf("agent-%03d", i), Title: fmt.Sprintf("Title %d", i), Branch: fmt.Sprintf("agentbox/agent-%03d", i), CreatedAt: base}
		if i == 7 {
			a.Title = "AÇÃO de graças"
		}
		var pr ArchivedPR
		if s := states[i%len(states)]; s != "" {
			pr = ArchivedPR{Number: 100 + i, Title: fmt.Sprintf("feat: thing %d", i), State: s}
		}
		if err := st.ArchiveAgent(ctx, a, fmt.Sprintf("Do thing %d", i), pr, base.Add(time.Duration(i/3)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
}

// The archive answers a page at a time what reading it whole answers, in the
// same order, for one project or every one, under a pull request state and a
// search, and never the same agent twice nor one skipped.
func TestArchivedPageWalksTheArchive(t *testing.T) {
	ctx := context.Background()
	st := openRemoveTest(t)
	seedArchive(t, st, 64)
	for _, q := range []ArchivedPageQuery{
		{},
		{Project: "pawly"},
		{PRState: PRMerged},
		{Project: "other", PRState: PRNone},
		{PRState: PROpen},
		{Query: "thing 1"},
		{Query: "Title", Project: "pawly", PRState: PRClosed},
	} {
		whole, err := st.ArchivedAgents(ctx, ArchivedQuery{Project: q.Project, PRState: q.PRState, Query: q.Query})
		if err != nil {
			t.Fatal(err)
		}
		var want []string
		for _, a := range whole {
			want = append(want, a.Ref())
		}
		var got []string
		q.Limit = 7
		for {
			page, counts, err := st.ArchivedPage(ctx, q)
			if err != nil {
				t.Fatalf("ArchivedPage(%+v): %v", q, err)
			}
			if counts.Of(q.PRState) != len(want) {
				t.Fatalf("ArchivedPage(%+v) counts %+v, and the archive keeps %d", q, counts, len(want))
			}
			if counts.All != counts.Open+counts.Merged+counts.Closed+counts.None {
				t.Fatalf("counts %+v don't add up", counts)
			}
			for _, a := range page {
				got = append(got, a.Ref())
			}
			if len(page) < q.Limit {
				break
			}
			last := page[len(page)-1]
			q.After = &ArchivedCursor{At: last.ArchivedAt, Project: last.Project, Name: last.Name}
		}
		if !slices.Equal(got, want) {
			t.Errorf("paging %+v gave\n%v\nand the whole archive is\n%v", q, got, want)
		}
	}
}

// A cursor that isn't a past agent's (a live agent's, between two seconds)
// comes before every agent archived in its second; the counts alone read no
// agent; and a search ignores the case of any letter, as the page's does.
func TestArchivedPageCursorsCountsAndSearch(t *testing.T) {
	ctx := context.Background()
	st := openRemoveTest(t)
	seedArchive(t, st, 30)
	at := time.Unix(1_800_000_004, 0)
	page, _, err := st.ArchivedPage(ctx, ArchivedPageQuery{Limit: 100, After: &ArchivedCursor{At: at, Through: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 15 || !page[0].ArchivedAt.Equal(at) {
		t.Errorf("after a cursor through second 4: %d agents from %v, want the 15 archived by then", len(page), page[0].ArchivedAt)
	}
	page, counts, err := st.ArchivedPage(ctx, ArchivedPageQuery{Project: "pawly"})
	if err != nil || len(page) != 0 || counts.All != 15 {
		t.Errorf("the counts alone = %d agents, %+v, %v; want none and 15 counted", len(page), counts, err)
	}
	page, counts, err = st.ArchivedPage(ctx, ArchivedPageQuery{Query: "ação", Limit: 10})
	if err != nil || len(page) != 1 || page[0].Name != "agent-007" || counts.All != 1 {
		t.Errorf("searching ação = %v, %+v, %v; want agent-007", page, counts, err)
	}
	if page, counts, err = st.ArchivedPage(ctx, ArchivedPageQuery{Query: "#102", Limit: 10}); err != nil || len(page) != 1 || page[0].PR.Number != 102 || counts.Open != 1 {
		t.Errorf("searching #102 = %v, %+v, %v", page, counts, err)
	}
	if _, _, err := st.ArchivedPage(ctx, ArchivedPageQuery{PRState: "x' OR 1", Limit: 1}); err == nil {
		t.Error("an unknown pull request state was taken")
	}
}

// The Work page's queries are answered from the indexes its migration adds:
// no sort of the whole archive for a page, no read of its rows for the counts.
func TestArchivedPageUsesItsIndexes(t *testing.T) {
	ctx := context.Background()
	st := openRemoveTest(t)
	plan := func(query string, args ...any) string {
		t.Helper()
		rows, err := st.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			out = append(out, detail)
		}
		return strings.Join(out, "; ")
	}
	for _, c := range []struct{ query, index string }{
		{`SELECT ` + archivedColumns + ` FROM archived_agents WHERE project = 'p' AND archived_at <= 5 AND (archived_at < 5 OR (project, name) > ('p', 'a')) ORDER BY archived_at DESC, project, name LIMIT 51`, "archived_agents_by_project_time"},
		{`SELECT ` + archivedColumns + ` FROM archived_agents WHERE archived_at <= 5 AND (archived_at < 5 OR (project, name) > ('p', 'a')) ORDER BY archived_at DESC, project, name LIMIT 51`, "archived_agents_by_time_name"},
		{`SELECT count(*), sum(pr_number > 0 AND pr_state = 'open'), sum(pr_number = 0) FROM archived_agents WHERE project = 'p'`, "COVERING INDEX archived_agents_by_pr"},
		{`SELECT ` + archivedColumns + ` FROM archived_agents WHERE pr_number > 0 AND pr_state = 'open' ORDER BY archived_at DESC, project, name`, "archived_agents_open_prs"},
	} {
		got := plan(c.query)
		if !strings.Contains(got, c.index) || strings.Contains(got, "TEMP B-TREE") {
			t.Errorf("%s\nis planned as %q, want it from %s with no sort", c.query, got, c.index)
		}
	}
}

// What a page's rows show of their agents' reviews and media is read for all
// of them at once, and is what reading each one answers.
func TestAgentReviewsAndMediaOfSeveral(t *testing.T) {
	ctx := context.Background()
	st := openRemoveTest(t)
	now := time.Now()
	var keys []AgentKey
	for i := range 900 { // more than one query's worth of variables
		k := AgentKey{Project: []string{"pawly", "other"}[i%2], Agent: fmt.Sprintf("agent-%03d", i)}
		keys = append(keys, k)
		if i%3 == 0 {
			if err := st.SetAgentReview(ctx, AgentReview{Project: k.Project, Agent: k.Agent, Verdict: "reviewed", Fingerprint: "f", At: now}); err != nil {
				t.Fatal(err)
			}
		}
		for m := range i % 4 {
			kind := []string{"screenshot", "screenshot", "recording"}[m]
			if err := st.AddMedia(ctx, Media{ID: fmt.Sprintf("m%d-%d", i, m), Project: k.Project, Agent: k.Agent, Kind: kind, Name: "n", CreatedAt: now}); err != nil {
				t.Fatal(err)
			}
		}
	}
	reviews, err := st.AgentReviewsOf(ctx, keys)
	if err != nil || len(reviews) != 300 {
		t.Fatalf("AgentReviewsOf = %d, %v; want 300", len(reviews), err)
	}
	if r := reviews[AgentKey{"pawly", "agent-006"}]; r.Verdict != "reviewed" || r.At.UnixMilli() != now.UnixMilli() {
		t.Errorf("agent-006's review = %+v", r)
	}
	media, err := st.MediaKindCounts(ctx, keys)
	if err != nil {
		t.Fatal(err)
	}
	if got := media[AgentKey{"other", "agent-003"}]; got["screenshot"] != 2 || got["recording"] != 1 {
		t.Errorf("agent-003's media = %v, want 2 screenshots and a recording", got)
	}
	if got, ok := media[AgentKey{"pawly", "agent-004"}]; ok {
		t.Errorf("agent-004 kept no media, and has %v", got)
	}
	// Another project's agent of the same name isn't this one.
	if _, ok := media[AgentKey{"pawly", "agent-003"}]; ok {
		t.Error("pawly/agent-003 was given other/agent-003's media")
	}
	if got, err := st.MediaKindCounts(ctx, nil); err != nil || len(got) != 0 {
		t.Errorf("no agents = %v, %v", got, err)
	}
}

// An install's archive from before the Work page's indexes is paged the same
// after the migration that adds them, which swaps the index on the time alone
// for the one that also carries the rest of the order.
func TestArchivePagingMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	at := slices.IndexFunc(migrations, func(m string) bool { return strings.Contains(m, "INDEX archived_agents_by_project_time ") })
	for i, m := range migrations[:at] {
		if _, err := db.ExecContext(ctx, m); err != nil {
			t.Fatalf("migration %d: %v", i+1, err)
		}
	}
	if _, err := db.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", at)); err != nil {
		t.Fatal(err)
	}
	for i := range 9 {
		if _, err := db.ExecContext(ctx, `INSERT INTO archived_agents (project, name, created_at, archived_at, pr_number, pr_state) VALUES ('old', ?, 1, ?, ?, ?)`,
			fmt.Sprintf("agent-%d", i), 100+i/2, i%3, []string{"", PROpen, PRMerged}[i%3]); err != nil {
			t.Fatal(err)
		}
	}
	_ = db.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	rows, err := st.db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'archived_agents' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var indexes []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		indexes = append(indexes, name)
	}
	_ = rows.Close()
	if want := []string{"archived_agents_by_pr", "archived_agents_by_project_time", "archived_agents_by_time_name", "archived_agents_open_prs"}; !slices.Equal(indexes, want) {
		t.Errorf("the archive's indexes = %v, want %v", indexes, want)
	}
	page, counts, err := st.ArchivedPage(ctx, ArchivedPageQuery{Project: "old", Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range page {
		names = append(names, a.Name)
	}
	if want := []string{"agent-8", "agent-6", "agent-7", "agent-4"}; !slices.Equal(names, want) {
		t.Errorf("the first page = %v, want %v", names, want)
	}
	if counts != (ArchivedCounts{All: 9, Open: 3, Merged: 3, None: 3}) {
		t.Errorf("counts = %+v", counts)
	}
}
