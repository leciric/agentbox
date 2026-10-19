package bridgecheck

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestRunProbe(t *testing.T) {
	spec := Spec{Addr4: "10.80.39.253/24", Gateway: "10.80.39.1", Addr6: "fd42::5/64", Gateway6: "fd42::1", MTU: 1400}
	var ran []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		ran = append(ran, name+" "+strings.Join(args, " "))
		if len(args) > 3 && args[3] == "/bin/agentbox" {
			return json.Marshal(good)
		}
		return nil, nil
	}
	r, err := RunProbe(context.Background(), spec, "/bin/agentbox", run)
	if err != nil || !reflect.DeepEqual(r, good) {
		t.Fatalf("RunProbe = %+v, %v", r, err)
	}
	all := strings.Join(ran, "\n")
	for _, want := range []string{
		"ip netns add agentbox-probe-",
		"type veth peer name eth0 mtu 1400 netns agentbox-probe-",
		"master incusbr0 up",
		"addr add 10.80.39.253/24 dev eth0",
		"route add default via 10.80.39.1",
		"addr add fd42::5/64 dev eth0 nodad",
		"-6 route add default via fd42::1",
		"/bin/agentbox host probe-bridge --inside {",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("ran no %q:\n%s", want, all)
		}
	}
	if !strings.HasPrefix(ran[len(ran)-1], "ip netns del agentbox-probe-") {
		t.Errorf("the namespace isn't removed last: %q", ran[len(ran)-1])
	}
}

func TestRunProbeFails(t *testing.T) {
	var ran []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		ran = append(ran, strings.Join(args, " "))
		if strings.Contains(strings.Join(args, " "), "master") {
			return nil, errors.New("ip: Cannot find device \"incusbr0\"")
		}
		return nil, nil
	}
	if _, err := RunProbe(context.Background(), Spec{Addr4: "10.0.0.2/24", Gateway: "10.0.0.1", MTU: 1500}, "/bin/agentbox", run); err == nil || !strings.Contains(err.Error(), "incusbr0") {
		t.Fatalf("RunProbe with no bridge = %v", err)
	}
	if !strings.HasPrefix(ran[len(ran)-1], "netns del") {
		t.Errorf("the namespace is left: %q", ran)
	}
}

func TestInside(t *testing.T) {
	old := dnsPort
	t.Cleanup(func() { dnsPort = old })
	dnsPort = serveDNS(t, dnsmessage.RCodeSuccess, false)
	page := strings.Repeat("x", downloadSize+100)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(page)) }))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	spec := Spec{Gateway: "127.0.0.1", Addr6: "fd42::5/64", Connect4: srv.Listener.Addr().String(), URL: srv.URL, Ping4: "127.0.0.1"}
	r := Inside(ctx, spec)
	if !r.DNS || !r.Connect4 || !r.Download4 || !r.V6 || r.Download6 {
		t.Errorf("Inside = %+v; want DNS, a connection and a download over IPv4, and none over IPv6 to an IPv4 address", r)
	}

	// Nothing listening: no connection, so no download tried.
	l, _ := net.Listen("tcp4", "127.0.0.1:0")
	closed := l.Addr().String()
	_ = l.Close()
	spec = Spec{Gateway: "127.0.0.1", Connect4: closed, URL: "http://" + closed + "/", Ping4: "not an address"}
	if r := Inside(ctx, spec); r.Connect4 || r.Download4 || r.Payload != 0 {
		t.Errorf("Inside with nothing listening = %+v", r)
	}
}

func TestDownloadsShortFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("short"))
	}))
	defer srv.Close()
	if !downloads(context.Background(), "tcp4", "127.0.0.1", srv.URL+"/") {
		t.Error("a whole short file isn't a download")
	}
	if downloads(context.Background(), "tcp4", "127.0.0.1", srv.URL+"/missing") {
		t.Error("a 404 is a download")
	}
}

func TestInsideAsksCandidates(t *testing.T) {
	old := dnsPort
	t.Cleanup(func() { dnsPort = old })
	dnsPort = serveDNS(t, dnsmessage.RCodeSuccess, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// The gateway can't serve DNS; of the candidates, only 127.0.0.1 has a server.
	r := Inside(ctx, Spec{Gateway: "192.0.2.1", Candidates: []string{"127.0.0.2", "127.0.0.1"}, Connect4: "127.0.0.1:1", Ping4: "x"})
	if !r.DNS || !reflect.DeepEqual(r.Answering, []string{"127.0.0.1"}) {
		t.Errorf("Inside = %+v; want DNS through 127.0.0.1 alone", r)
	}
	r = Inside(ctx, Spec{Gateway: "127.0.0.1", Candidates: []string{"127.0.0.2"}, Connect4: "127.0.0.1:1", Ping4: "x"})
	if r.DNS || len(r.Answering) > 0 {
		t.Errorf("Inside with no candidate answering = %+v; want no DNS, even with the gateway answering", r)
	}
}
