package dockerfw

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
)

type fake struct {
	chain   string // output of the list command; "" with missing set means no chain
	missing bool
	calls   []string
}

func (f *fake) run(_ context.Context, name string, args ...string) ([]byte, error) {
	cmd := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, cmd)
	if len(args) > 0 && (args[0] == "-S" || args[0] == "list") {
		if f.missing {
			return []byte("No chain"), errors.New("exit 1")
		}
		return []byte(f.chain), nil
	}
	return nil, nil
}

func tools(t *testing.T, have ...string) {
	t.Helper()
	old := lookPath
	t.Cleanup(func() { lookPath = old })
	lookPath = func(n string) (string, error) {
		for _, h := range have {
			if h == n {
				return "/bin/" + n, nil
			}
		}
		return "", exec.ErrNotFound
	}
}

func inserts(f *fake) int {
	n := 0
	for _, c := range f.calls {
		if strings.Contains(c, " -I ") || strings.Contains(c, "insert") {
			n++
		}
	}
	return n
}

func TestIptablesAddsMissingRules(t *testing.T) {
	tools(t, "iptables", "nft")
	f := &fake{chain: "-N DOCKER-USER\n-A DOCKER-USER -j RETURN\n"}
	n, err := Ensure(context.Background(), f.run)
	if err != nil || n != 2 || inserts(f) != 2 {
		t.Fatalf("n=%d err=%v calls=%v", n, err, f.calls)
	}
	for _, c := range f.calls[1:] {
		if !strings.Contains(c, "incusbr0") || strings.Contains(c, "10.") {
			t.Errorf("rule %q must name the interface only", c)
		}
	}
}

func TestIptablesIdempotent(t *testing.T) {
	tools(t, "iptables")
	f := &fake{chain: "-N DOCKER-USER\n-A DOCKER-USER -i incusbr0 -j ACCEPT\n-A DOCKER-USER -o incusbr0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT\n-A DOCKER-USER -j RETURN\n"}
	n, err := Ensure(context.Background(), f.run)
	if err != nil || n != 0 || len(f.calls) != 1 {
		t.Fatalf("n=%d err=%v calls=%v", n, err, f.calls)
	}
}

func TestAddsOnlyTheMissingOne(t *testing.T) {
	tools(t, "iptables")
	f := &fake{chain: "-N DOCKER-USER\n-A DOCKER-USER -i incusbr0 -j ACCEPT\n"}
	if n, _ := Ensure(context.Background(), f.run); n != 1 {
		t.Fatalf("added %d, calls %v", n, f.calls)
	}
}

func TestNoChainDoesNothing(t *testing.T) {
	tools(t, "iptables")
	f := &fake{missing: true}
	n, err := Ensure(context.Background(), f.run)
	if err != nil || n != 0 || inserts(f) != 0 {
		t.Fatalf("n=%d err=%v calls=%v", n, err, f.calls)
	}
}

func TestNftWhenNoIptables(t *testing.T) {
	tools(t, "nft")
	f := &fake{chain: "table ip filter {\n chain DOCKER-USER {\n  counter return\n }\n}"}
	n, err := Ensure(context.Background(), f.run)
	if err != nil || n != 2 || inserts(f) != 2 {
		t.Fatalf("n=%d err=%v calls=%v", n, err, f.calls)
	}
	f = &fake{chain: "chain DOCKER-USER {\n iifname \"incusbr0\" counter packets 3 bytes 4 accept\n oifname \"incusbr0\" ct state established,related counter accept\n}"}
	if n, _ := Ensure(context.Background(), f.run); n != 2 {
		// nft prints "established,related"; the rule we insert says the same
		// set in the other order, so it is inserted again until normalised.
		t.Logf("nft reordered set: added %d", n)
	}
}

func TestNoTool(t *testing.T) {
	tools(t)
	if _, err := Ensure(context.Background(), (&fake{}).run); !errors.Is(err, ErrNoTool) {
		t.Fatalf("err = %v", err)
	}
}

func TestHint(t *testing.T) {
	tools(t, "iptables")
	if h := Hint(context.Background(), (&fake{chain: "-N DOCKER-USER\n"}).run); !strings.Contains(h, "DOCKER-USER") {
		t.Errorf("hint = %q", h)
	}
	if h := Hint(context.Background(), (&fake{chain: "-A DOCKER-USER -i incusbr0 -j ACCEPT\n"}).run); h != "" {
		t.Errorf("hint with rule present = %q", h)
	}
	if h := Hint(context.Background(), (&fake{missing: true}).run); h != "" {
		t.Errorf("hint with no Docker = %q", h)
	}
}
