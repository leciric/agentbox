package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
)

// fakePage answers the DevTools protocol for one page, as the picker uses it:
// poll answers the overlay's state in turn, and a world made before a
// navigation (navigateAfter polls) is gone after it.
type fakePage struct {
	mu       sync.Mutex
	polls    []string
	worlds   int
	installs int
	cancels  int
	shots    []string
}

func (f *fakePage) serve(t *testing.T) string {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			var msg struct {
				ID     int
				Method string
				Params map[string]any
			}
			_ = json.Unmarshal(data, &msg)
			result, failure := f.answer(msg.Method, msg.Params)
			reply := map[string]any{"id": msg.ID, "result": result}
			if failure != "" {
				reply = map[string]any{"id": msg.ID, "error": map[string]any{"message": failure}}
			}
			out, _ := json.Marshal(reply)
			_ = conn.Write(r.Context(), websocket.MessageText, out)
		}
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func (f *fakePage) answer(method string, params map[string]any) (any, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	value := func(v string) any {
		return map[string]any{"result": map[string]any{"type": "object", "value": json.RawMessage(v)}}
	}
	switch method {
	case "Page.getFrameTree":
		return map[string]any{"frameTree": map[string]any{"frame": map[string]any{"id": "main"}}}, ""
	case "Page.createIsolatedWorld":
		f.worlds++
		return map[string]any{"executionContextId": f.worlds}, ""
	case "Page.captureScreenshot":
		f.shots = append(f.shots, params["format"].(string))
		return map[string]any{"data": "iVBORw0KGgo="}, ""
	case "Runtime.evaluate":
		expr := params["expression"].(string)
		switch {
		case expr == "document.visibilityState":
			return value(`"visible"`), ""
		case strings.Contains(expr, "agentbox-picker"):
			f.installs++
			return map[string]any{"result": map[string]any{"type": "undefined"}}, ""
		case expr == "globalThis.__agentboxPicker?.cancel()":
			f.cancels++
			return value("null"), ""
		case expr == pickResult:
			if len(f.polls) == 0 {
				return value("0"), ""
			}
			next := f.polls[0]
			f.polls = f.polls[1:]
			if next == "gone" {
				return nil, "Cannot find context with specified id"
			}
			return value(next), ""
		}
		return value("null"), ""
	}
	return nil, "unknown method " + method
}

func TestPickElementWaitsForTheClickAndCrops(t *testing.T) {
	t.Parallel()
	f := &fakePage{polls: []string{"0", "gone", `{"url":"http://app/","selector":"#go","tag":"button","html":"<button id=\"go\">Go</button>","styles":[{"name":"color","value":"red"}],"box":{"x":10,"y":20,"width":100,"height":30},"viewport":{"width":800,"height":600},"scroll":{"x":0,"y":500}}`}}
	ws := f.serve(t)
	el, err := pickElement(context.Background(), http.DefaultClient, []BrowserPage{{webSocket: ws}}, "Pick")
	if err != nil {
		t.Fatal(err)
	}
	if el == nil || el.Selector != "#go" || el.Box.Width != 100 || len(el.Styles) != 1 {
		t.Fatalf("picked %+v", el)
	}
	if el.Image == nil || el.Image.MimeType != "image/png" {
		t.Errorf("crop %+v, want a PNG", el.Image)
	}
	if f.installs != 2 || f.worlds != 2 {
		t.Errorf("installed %d times in %d worlds, want again after the navigation", f.installs, f.worlds)
	}
}

func TestPickElementCancelled(t *testing.T) {
	t.Parallel()
	f := &fakePage{polls: []string{"false"}}
	el, err := pickElement(context.Background(), http.DefaultClient, []BrowserPage{{webSocket: f.serve(t)}}, "")
	if el != nil || err != nil {
		t.Fatalf("Escape: %v, %v, want nothing", el, err)
	}

	// Cancelled from the app: the overlay is taken off the page.
	f = &fakePage{}
	ws := f.serve(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*pickPoll)
	defer cancel()
	el, err = pickElement(ctx, http.DefaultClient, []BrowserPage{{webSocket: ws}}, "")
	if el != nil || err != nil {
		t.Fatalf("cancelled: %v, %v, want nothing", el, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cancels != 1 {
		t.Errorf("the overlay was cancelled %d times, want once", f.cancels)
	}
}

func TestPickElementOffScreenHasNoPicture(t *testing.T) {
	t.Parallel()
	f := &fakePage{polls: []string{`{"selector":"#far","box":{"x":10,"y":900,"width":100,"height":30},"viewport":{"width":800,"height":600}}`}}
	el, err := pickElement(context.Background(), http.DefaultClient, []BrowserPage{{webSocket: f.serve(t)}}, "")
	if err != nil || el == nil {
		t.Fatal(el, err)
	}
	if el.Image != nil || len(f.shots) != 0 {
		t.Errorf("an element out of view got a picture: %+v", el.Image)
	}
}

func TestPickElementNeedsAPage(t *testing.T) {
	t.Parallel()
	if _, err := pickElement(context.Background(), http.DefaultClient, []BrowserPage{{webSocket: "ws://127.0.0.1:1/nothing"}}, ""); err == nil {
		t.Error("no page to connect to: no error")
	}
}
