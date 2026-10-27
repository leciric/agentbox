package k8s_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"agentbox/internal/k8s"
)

// event is one the cluster wrote about an object, in the shape the API server
// hands back.
func event(name, kind, reason, message string, at time.Time) *corev1.Event {
	return &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "agentbox"},
		InvolvedObject: corev1.ObjectReference{
			Kind: kind,
			Name: "ab-pawly-agent-01",
		},
		Type:          "Warning",
		Reason:        reason,
		Message:       message,
		LastTimestamp: metav1.NewTime(at),
	}
}

// TestEventsAreTheNamespacesNewestFirst is what the cluster's page shows as its
// last events: newest first, named by the object they are about, including the
// ones about a node, which are what explain a pod that stayed Pending.
func TestEventsAreTheNamespacesNewestFirst(t *testing.T) {
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	older := event("pod.old", "Pod", "FailedScheduling", "0/1 nodes are available: 1 Insufficient memory", now.Add(-10*time.Minute))
	newest := event("node.new", "Node", "NodeHasNoDiskPressure", "node had no room for another pod", now)
	// A repeat is one event with a count, and the count lives in its series on
	// a newer API server.
	repeated := event("pod.repeat", "Pod", "BackOff", "Back-off restarting failed container", now.Add(-5*time.Minute))
	repeated.Count = 3
	// A version that only fills EventTime: the fallback is what puts it in
	// order.
	micro := event("pod.micro", "Pod", "Pulled", "Container image already present on machine", time.Time{})
	micro.LastTimestamp = metav1.Time{}
	micro.EventTime = metav1.NewMicroTime(now.Add(-2 * time.Minute))
	micro.FirstTimestamp = metav1.Time{}

	client := fake.NewClientset(older, newest, repeated, micro)
	cluster := k8s.FromClient("k3d", "", client)
	events, err := cluster.Events(ctx)
	if err != nil {
		t.Fatalf("Events() = %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("Events() = %+v, want all four", events)
	}
	if events[0].Reason != "NodeHasNoDiskPressure" || events[1].Reason != "Pulled" || events[2].Reason != "BackOff" || events[3].Reason != "FailedScheduling" {
		t.Errorf("Events() = %v, want them newest first", []string{events[0].Reason, events[1].Reason, events[2].Reason, events[3].Reason})
	}
	if got := events[3].Object; got != "Pod/ab-pawly-agent-01" {
		t.Errorf("an event's object = %q, want Kind/Name", got)
	}
	if got := events[2].Count; got != 3 {
		t.Errorf("a repeated event's count = %d, want 3", got)
	}
	if got := events[0].Message; got == "" {
		t.Error("an event's message was dropped")
	}
}

// TestEventsAreCappedAtFifty keeps one read bounded: a namespace that has been
// running for a while has thousands, and the page shows what just happened.
func TestEventsAreCappedAtFifty(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	objects := make([]*corev1.Event, 0, 55)
	for i := range 55 {
		objects = append(objects, event(fmt.Sprintf("e-%02d", i), "Pod", fmt.Sprintf("Reason%02d", i), "message", now.Add(time.Duration(i)*time.Minute)))
	}
	client := fake.NewClientset()
	for _, object := range objects {
		if _, err := client.CoreV1().Events("agentbox").Create(context.Background(), object, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	cluster := k8s.FromClient("k3d", "", client)
	events, err := cluster.Events(context.Background())
	if err != nil {
		t.Fatalf("Events() = %v", err)
	}
	if len(events) != 50 {
		t.Fatalf("%d events, want the newest 50", len(events))
	}
	if events[0].Reason != "Reason54" {
		t.Errorf("the first event = %q, want the newest", events[0].Reason)
	}
}
