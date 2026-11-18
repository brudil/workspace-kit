package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/withworkspace/plugin"
	"github.com/withworkspace/plugin/plugintest"
	"github.com/withworkspace/plugin/protocol"
)

func TestHello(t *testing.T) {
	ws := plugintest.New(t, files, setup, plugintest.Name("hello"))

	reg := ws.Registration()
	if len(reg.Views) != 1 || reg.Views[0].Title != "Hello" || reg.Views[0].Path != "/" {
		t.Errorf("views registered: %+v", reg.Views)
	}
	if reg.Agent == nil || !strings.Contains(reg.Agent.Guide, "ws hello say") {
		t.Errorf("the guide wasn't taken from agents.md: %+v", reg.Agent)
	}

	// The app, as built and committed, is what ws is told to load, and
	// it's served as what it is. The plugin serves no page at its view.
	if reg.UI == nil || !strings.HasPrefix(reg.UI.Script, "/ui/app.js?v=") || !strings.HasPrefix(reg.UI.Style, "/ui/app.css?v=") {
		t.Fatalf("the app registered: %+v", reg.UI)
	}
	if js := ws.Get(reg.UI.Script); js.Status != 200 || !strings.HasPrefix(js.Header.Get("Content-Type"), "text/javascript") {
		t.Errorf("the app's entry: %d, %q", js.Status, js.Header.Get("Content-Type"))
	}
	if css := ws.Get(reg.UI.Style); css.Status != 200 || !strings.HasPrefix(css.Header.Get("Content-Type"), "text/css") {
		t.Errorf("the app's stylesheet: %d, %q", css.Status, css.Header.Get("Content-Type"))
	}
	if page := ws.Get("/"); page.Status != 404 {
		t.Errorf("the plugin answered at its view's path, which is ws's to serve: %d\n%s", page.Status, page.Body)
	}

	if got := ws.Run("say"); got.Stdout != "Hello, workspace\n" || got.Exit != 0 {
		t.Errorf("say: %+v", got)
	}
	if got := ws.Run("say", "there", "-f", "json"); !strings.Contains(got.Stdout, `"message": "Hello, there"`) {
		t.Errorf("say -f json: %+v", got)
	}

	// Each look at the app is counted, in the plugin's own database.
	for range 2 {
		if got := ws.Post("/api/visits", nil); got.Status != 200 {
			t.Fatalf("a visit: %d\n%s", got.Status, got.Body)
		}
	}
	if got := ws.Post("/api/visits", nil); strings.TrimSpace(got.Body) != `{"greeting":"Hello","visits":3}` {
		t.Errorf("the third visit: %s", got.Body)
	}
	if got := ws.Run("visits"); got.Stdout != "Visits:  3\n" {
		t.Errorf("visits: %+v", got)
	}

	// Each visit has a name, which ws finds in what's written, and is
	// made of what the plugin says: one that's there is shown, and one
	// that isn't is said to be.
	if refs := reg.Refs; len(refs) != 1 || refs[0].Kind != "visit" || refs[0].Prefixes[0] != "VISIT-" || len(refs[0].Fields) != 3 || len(refs[0].Views["pane"]) != 4 {
		t.Errorf("refs registered: %+v", refs)
	}
	if got, err := ws.Show("visit", "VISIT-3", false); err != nil || got.Fields["title"] != "Visit 3" || got.Fields["at"] == "" || !strings.Contains(got.Fields["said"], "said: **Hello**.") {
		t.Errorf("VISIT-3: %+v, %v", got, err)
	}
	if _, err := ws.Show("visit", "VISIT-4", false); !errors.Is(err, plugin.ErrNotFound) {
		t.Errorf("VISIT-4, which hasn't happened: %v", err)
	}
	// ws was told of each as it happened: it has them without asking.
	if told := ws.Things("visit"); len(told) != 3 || told[2].Ref != "VISIT-3" || told[2].Fields["title"] != "Visit 3" {
		t.Errorf("visits ws was told of: %+v", told)
	}

	// A change of config reaches the next read, with no restart, and the
	// open app is told to ask again.
	if err := ws.SetConfig(map[string]any{"greeting": "Howdy"}); err != nil {
		t.Fatal(err)
	}
	if got := ws.Run("say"); got.Stdout != "Howdy, workspace\n" {
		t.Errorf("say after the greeting changed: %+v", got)
	}
	if got := ws.Get("/api/hello"); strings.TrimSpace(got.Body) != `{"greeting":"Howdy","visits":3}` {
		t.Errorf("what the app is told after the greeting changed: %s", got.Body)
	}
	if n := len(ws.Notifications(protocol.MethodViewChanged)); n != 1 {
		t.Errorf("the open app was told %d times that what it shows is stale", n)
	}
}
