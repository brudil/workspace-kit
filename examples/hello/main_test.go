package main

import (
	"strings"
	"testing"

	"github.com/withworkspace/plugin/plugintest"
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

	if got := ws.Run("say"); got.Stdout != "Hello, workspace\n" || got.Exit != 0 {
		t.Errorf("say: %+v", got)
	}
	if got := ws.Run("say", "there", "-f", "json"); !strings.Contains(got.Stdout, `"message": "Hello, there"`) {
		t.Errorf("say -f json: %+v", got)
	}

	// Each look at the page is counted, in the plugin's own database.
	for range 2 {
		if page := ws.Get("/"); page.Status != 200 || !strings.Contains(page.Body, "<h1>Hello, workspace</h1>") {
			t.Fatalf("the page: %d\n%s", page.Status, page.Body)
		}
	}
	if page := ws.Get("/"); !strings.Contains(page.Body, "Seen 3 times.") {
		t.Errorf("the page didn't count three visits:\n%s", page.Body)
	}
	if got := ws.Run("visits"); got.Stdout != "Visits:  3\n" {
		t.Errorf("visits: %+v", got)
	}

	// A change of config reaches the next read, with no restart.
	if err := ws.SetConfig(map[string]any{"greeting": "Howdy"}); err != nil {
		t.Fatal(err)
	}
	if got := ws.Run("say"); got.Stdout != "Howdy, workspace\n" {
		t.Errorf("say after the greeting changed: %+v", got)
	}
}
