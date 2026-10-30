package k8s

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// eventsListed is how many events one read returns: a namespace's event history
// is mostly noise by the time it is old, and the page shows what just happened.
const eventsListed = 50

// Event is one of the cluster's own events about an object in the namespace:
// why a pod didn't start, what a node ran out of.
type Event struct {
	// Type is the cluster's: Normal or Warning.
	Type string
	// Reason is its short word for it, like FailedScheduling or Pulled.
	Reason string
	// Object is the object it is about, as Kind/Name.
	Object string
	// Message is the sentence it wrote, which is what usually says what to do.
	Message string
	// Count is how many times it happened: the cluster folds repeats into one
	// event rather than keeping every one.
	Count int
	At    time.Time
}

// Events is the namespace's recent events, newest first, capped at
// eventsListed. It reads every event in the namespace rather than only
// AgentBox's: a node saying it has no room, or a volume that can't bind, is
// about no object of ours and is exactly what explains a pod that stays
// Pending.
func (c *Cluster) Events(ctx context.Context) ([]Event, error) {
	events, err := c.client.CoreV1().Events(c.namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing the events of cluster %s: %w", c.name, err)
	}
	out := make([]Event, 0, len(events.Items))
	for i := range events.Items {
		out = append(out, eventFrom(&events.Items[i]))
	}
	slices.SortStableFunc(out, func(a, b Event) int {
		// Newest first; the tie-break keeps a page stable between reads.
		if byTime := b.At.Compare(a.At); byTime != 0 {
			return byTime
		}
		if byReason := cmp.Compare(a.Reason, b.Reason); byReason != 0 {
			return byReason
		}
		return cmp.Compare(a.Object, b.Object)
	})
	if len(out) > eventsListed {
		out = out[:eventsListed]
	}
	return out, nil
}

// eventFrom is the part of an event the page shows. A cluster writes the time
// in whichever of three fields its version fills in, so the last one that is set
// is the one to sort by.
func eventFrom(event *corev1.Event) Event {
	count := int(event.Count)
	if event.Series != nil && int(event.Series.Count) > count {
		count = int(event.Series.Count)
	}
	return Event{
		Type:    event.Type,
		Reason:  event.Reason,
		Object:  event.InvolvedObject.Kind + "/" + event.InvolvedObject.Name,
		Message: event.Message,
		Count:   count,
		At:      eventTime(event),
	}
}

// eventTime is when an event last happened, in the first field its version
// filled in.
func eventTime(event *corev1.Event) time.Time {
	if !event.LastTimestamp.IsZero() {
		return event.LastTimestamp.Time
	}
	if !event.EventTime.IsZero() {
		return event.EventTime.Time
	}
	if !event.FirstTimestamp.IsZero() {
		return event.FirstTimestamp.Time
	}
	if event.Series != nil && !event.Series.LastObservedTime.IsZero() {
		return event.Series.LastObservedTime.Time
	}
	return event.CreationTimestamp.Time
}
