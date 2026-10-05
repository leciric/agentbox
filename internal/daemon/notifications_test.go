package daemon

import (
	"context"
	"testing"
	"time"

	"agentbox/internal/api"
	"agentbox/internal/memory"
	"agentbox/internal/state"
)

// A question the lead answers itself isn't the user's to hear about; one it
// passes on is, as are a finish (with what the agent last reported) and a
// screenshot, which the all-projects Media view marks unseen until either
// it or the bell says it was seen.
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

	if _, err := d.srv.memory().AddReport(ctx, memory.Report{Project: "hello-stack", Agent: "agent-01", Status: "partial", Summary: "Built the page; paging is left."}); err != nil {
		t.Fatal(err)
	}
	d.srv.notifyFinished(ctx, api.AgentEvent{Project: "hello-stack", Agent: "agent-01", Title: "Reminders page", Summary: "Done.", At: time.Now()})

	shot := state.Media{ID: "shot-1", Project: "hello-stack", Agent: "agent-01", Kind: "screenshot", Name: "the page", Source: "agent", Meta: "{}", CreatedAt: time.Now()}
	if err := d.srv.store.AddMedia(ctx, shot); err != nil {
		t.Fatal(err)
	}
	d.srv.notifyMedia(ctx, a, toAPIMedia(shot, ""))
	d.srv.notifyMedia(ctx, a, api.MediaItem{ID: "note-1", Kind: "note"}) // not worth a notification

	list, err := d.client.Notifications(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("Notifications() = %d, want 3: %+v", len(list), list)
	}
	media, finished, question := list[0], list[1], list[2]
	if media.Kind != api.NotifyMedia || media.Media == nil || media.Media.ID != "shot-1" || media.Media.AgentTitle != "Reminders page" || media.Ref != "hello-stack/agent-01" {
		t.Errorf("media notification = %+v", media)
	}
	if finished.Kind != api.NotifyFinished || finished.Status != "partial" || finished.Text != "Built the page; paging is left." {
		t.Errorf("finished notification = %+v, want the report's status and summary", finished)
	}
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

	// A deleted item's notification stays, saying so.
	if err := d.srv.store.DeleteMedia(ctx, "shot-1"); err != nil {
		t.Fatal(err)
	}
	if list, _ := d.client.Notifications(ctx); !list[0].Media.Removed || !list[0].Seen {
		t.Errorf("notification of a deleted item = %+v, want removed and seen", list[0].Media)
	}
}
