package chat

import (
	"context"
	"strings"
	"testing"

	"agentbox/internal/api"
)

// A message a paired AgentBox sent keeps its author, in the chat and in what
// is stored of it; one sent on this machine has none.
func TestAMessageKeepsItsAuthor(t *testing.T) {
	t.Parallel()
	store := openStore(t)
	m, _ := newManager(t, store, newFakeTool(answerHello))
	ana := &api.Person{ID: "peer_ab12", Name: "Ana"}
	if _, err := m.SendMessage(testAgent, api.ChatMessageRequest{Text: "hi from Ana", Author: ana}); err != nil {
		t.Fatal(err)
	}
	th := waitThread(t, m, testAgent, "Ana's turn", turnsEnded(1))
	if got := find(th, "user", 0); got.Author == nil || *got.Author != *ana {
		t.Errorf("Ana's message has author %+v, want %+v", got.Author, ana)
	}
	if _, err := m.Send(testAgent, "and mine"); err != nil {
		t.Fatal(err)
	}
	th = waitThread(t, m, testAgent, "my turn", turnsEnded(2))
	if got := find(th, "user", 1); got.Author != nil {
		t.Errorf("a message sent here has author %+v, want none", got.Author)
	}
	items, err := store.ChatItems(context.Background(), testAgent.Project, testAgent.Name)
	if err != nil {
		t.Fatal(err)
	}
	stored := 0
	for _, it := range items {
		if strings.Contains(string(it.Data), `"author":{"id":"peer_ab12","name":"Ana"}`) {
			stored++
		}
	}
	if stored != 1 {
		t.Errorf("%d stored items carry Ana as author, want her one message", stored)
	}
}
