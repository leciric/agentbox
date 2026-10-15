package daemon

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/state"
)

// A question the lead answers itself isn't the user's to hear about; one it
// passes on is. A screenshot is no inbox item, but the all-projects Media
// view marks it unseen until it or the inbox says it was seen.
func TestNotificationsReachTheBellAndTheMediaView(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")

	asked := make(chan api.Question, 1)
	ask := func(text string) string {
		go func() {
			q, err := d.srv.askForTest(ctx, a, text, "")
			if err != nil {
				t.Errorf("ask: %v", err)
			}
			asked <- q
		}()
		return waitForQuestion(t, d, "hello-stack")
	}
	id := ask("Should the page paginate?")
	if _, err := d.srv.answerQuestion(ctx, id, "Yes.", "lead"); err != nil {
		t.Fatal(err)
	}
	<-asked
	if list, err := d.client.Notifications(ctx); err != nil || len(list) != 0 {
		t.Fatalf("Notifications() after the lead answered = %+v, %v; want none", list, err)
	}

	id = ask("Which payment provider?")
	lead := api.NewClient(d.srv.leadSocketPath("hello-stack"))
	if _, err := lead.LeadEscalateQuestion(ctx, id, "a product decision"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.client.AnswerQuestion(ctx, "hello-stack", id, "Stripe."); err != nil {
		t.Fatal(err)
	}
	<-asked

	shot := state.Media{ID: "shot-1", Project: "hello-stack", Agent: "agent-01", Kind: "screenshot", Name: "the page", Source: "agent", Meta: "{}", CreatedAt: time.Now()}
	if err := d.srv.store.AddMedia(ctx, shot); err != nil {
		t.Fatal(err)
	}
	d.srv.notifyMedia(ctx, a, toAPIMedia(shot, ""))
	d.srv.notifyMedia(ctx, a, api.MediaItem{ID: "note-1", Kind: "note"}) // not worth a notification

	// The screenshot is kept as the Media view's unseen mark, not listed.
	list, err := d.client.Notifications(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("Notifications() = %d, want just the question: %+v", len(list), list)
	}
	question := list[0]
	if question.Kind != api.NotifyQuestion || question.Question != id || question.Text != "Which payment provider?" {
		t.Errorf("question notification = %+v", question)
	}

	items, err := d.client.AllMedia(ctx, "screenshot", "recording")
	if err != nil || len(items) != 1 || !items[0].Unseen || items[0].AgentName != "agent-01" {
		t.Fatalf("AllMedia() = %+v, %v; want shot-1, unseen", items, err)
	}
	if res, err := d.client.SeeNotifications(ctx, api.SeeNotificationsRequest{Media: []string{"shot-1"}}); err != nil || res.Seen != 1 {
		t.Fatalf("SeeNotifications(shot-1) = %+v, %v", res, err)
	}
	if items, _ := d.client.AllMedia(ctx); len(items) != 1 || items[0].Unseen {
		t.Errorf("AllMedia() after seeing = %+v, want shot-1 seen", items)
	}

}

// The inbox reads its questions a page at a time, newest first, with the
// total and the unseen ones apart; "Mark all read" marks every question, not
// only the pages loaded, and leaves the Media views' marks alone.
func TestNotificationsArePaged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	d := startTestDaemon(t, t.TempDir(), fakeIncus)
	repo := d.fixtureRepo(t, "hello-stack")
	if _, err := d.client.AddProject(ctx, api.AddProjectRequest{Path: repo}); err != nil {
		t.Fatal(err)
	}
	a := addAgent(t, d, repo, "hello-stack", "agent-01", "Reminders page")
	now := time.Now()
	for i := range 5 {
		d.srv.notify(ctx, api.Notification{Kind: api.NotifyQuestion, Project: a.Project, Agent: a.Name, Text: fmt.Sprintf("q%d", i), At: now.Add(time.Duration(i) * time.Minute)})
	}
	shot := state.Media{ID: "shot-1", Project: a.Project, Agent: a.Name, Kind: "screenshot", Name: "page", Source: "agent", Meta: "{}", CreatedAt: now}
	if err := d.srv.store.AddMedia(ctx, shot); err != nil {
		t.Fatal(err)
	}
	d.srv.notifyMedia(ctx, a, toAPIMedia(shot, ""))

	var texts []string
	cursor := ""
	for pages := 0; pages < 5; pages++ {
		q := url.Values{"limit": {"2"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		page, err := d.client.NotificationPage(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 5 {
			t.Errorf("page %d total = %d, want 5 questions, not the screenshot", pages, page.Total)
		}
		for _, n := range page.Items {
			texts = append(texts, n.Text)
		}
		if cursor = page.Next; cursor == "" {
			break
		}
	}
	if got := strings.Join(texts, ","); got != "q4,q3,q2,q1,q0" {
		t.Errorf("pages = %s, want every question once, newest first", got)
	}

	res, err := d.client.SeeNotifications(ctx, api.SeeNotificationsRequest{Kind: api.NotifyQuestion})
	if err != nil || res.Seen != 5 {
		t.Fatalf("SeeNotifications(question) = %+v, %v; want 5", res, err)
	}
	if page, _ := d.client.NotificationPage(ctx, url.Values{"limit": {"2"}, "unseen": {"1"}}); page.Total != 0 || len(page.Items) != 0 {
		t.Errorf("unseen after marking all = %+v, want none", page)
	}
	if items, _ := d.client.AllMedia(ctx); len(items) != 1 || !items[0].Unseen {
		t.Errorf("AllMedia() = %+v, want the screenshot still unseen", items)
	}
}
