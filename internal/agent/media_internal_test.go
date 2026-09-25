package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

func TestJUnitCountsCountsTestCases(t *testing.T) {
	cases := map[string]struct {
		xml  string
		want *TestCounts
	}{
		// Node's test runner puts top-level tests straight under <testsuites>.
		"node": {`<?xml version="1.0" encoding="utf-8"?>
<testsuites>
	<testcase name="the home route says hello" time="0.038" classname="test"/>
	<testcase name="the login page asks for a name" time="0.001" classname="test"/>
	<testcase name="a broken one" time="0.001" classname="test">
		<failure type="testCodeFailure" message="boom">AssertionError</failure>
	</testcase>
	<!-- tests 3 -->
	<!-- pass 2 -->
	<!-- fail 1 -->
</testsuites>`, &TestCounts{Passed: 2, Failed: 1}},
		// Nested suites repeat their children's counts: adding them up would count twice.
		"nested suites": {`<testsuites tests="3" failures="1">
  <testsuite name="outer" tests="3" failures="1" skipped="1">
    <testsuite name="inner" tests="2" failures="1">
      <testcase name="a"/>
      <testcase name="b"><error message="x"/></testcase>
    </testsuite>
    <testcase name="c"><skipped/></testcase>
  </testsuite>
</testsuites>`, &TestCounts{Passed: 1, Failed: 1, Skipped: 1}},
		"not junit": {`<project><name>pawly</name></project>`, nil},
	}
	for name, c := range cases {
		file := filepath.Join(t.TempDir(), "results.xml")
		if err := os.WriteFile(file, []byte(c.xml), 0o644); err != nil {
			t.Fatal(err)
		}
		got := junitCounts(file)
		if (got == nil) != (c.want == nil) || got != nil && *got != *c.want {
			t.Errorf("%s: junitCounts = %+v, want %+v", name, got, c.want)
		}
	}
}

// TestDockSizeMatchesBrowserScript keeps dockHeight and dockMargin in step
// with browser.sh: a desktop recording's key captions are centred on the dock,
// so a dock of another size with stale constants would put them off it, over
// the windows above.
func TestDockSizeMatchesBrowserScript(t *testing.T) {
	script := string(browserScript)
	size := regexp.MustCompile(`(?m)^panel_size = \S+ (\d+)$`).FindStringSubmatch(script)
	margin := regexp.MustCompile(`(?m)^panel_margin = \S+ (\d+)$`).FindStringSubmatch(script)
	if size == nil || margin == nil {
		t.Fatalf("browser.sh has no panel_size and panel_margin to read: %q, %q", size, margin)
	}
	if height, _ := strconv.Atoi(size[1]); height != dockHeight {
		t.Errorf("the dock is %d px tall, dockHeight is %d", height, dockHeight)
	}
	if below, _ := strconv.Atoi(margin[1]); below != dockMargin {
		t.Errorf("the dock sits %d px above the bottom, dockMargin is %d", below, dockMargin)
	}
}
