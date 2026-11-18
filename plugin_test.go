package plugin_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/withworkspace/plugin"
	"github.com/withworkspace/plugin/plugintest"
	"github.com/withworkspace/plugin/protocol"
)

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

// The app as built, at its smallest: its entry and its stylesheet.
const (
	appJS  = `console.log("the app")`
	appCSS = `main { color: var(--ws-fg) }`
)

// hashed is a file of the app's as the registration should name it.
func hashed(name, content string) string {
	sum := sha256.Sum256([]byte(content))
	return "/ui/" + name + "?v=" + hex.EncodeToString(sum[:4])
}

// --- registration ---

func TestRegistrationIsWhatWasDeclared(t *testing.T) {
	files := fstest.MapFS{
		"agents.md":       file("Use `ws jira mine`.\n"),
		"ui/dist/app.js":  file(appJS),
		"ui/dist/app.css": file(appCSS),
	}
	ws := plugintest.New(t, files, func(p *plugin.Plugin) {
		p.Describe("Jira issues beside your missions")
		p.Config.Version(2)
		p.Config.URL("site", "Jira site", plugin.Required, plugin.Example("https://acme.atlassian.net"))
		p.Config.Secret("token", "API token", plugin.Required, plugin.Help("From id.atlassian.com"))
		p.Config.List("projects", "Projects to show", plugin.Default([]string{}))
		p.Config.Enum("kind", "Kind", []string{"a", "b"}, plugin.Default("a"))
		p.Config.String("email", "Your email", plugin.Local)
		p.UI("ui/dist")
		p.View("Jira", plugin.Icon("ticket"), plugin.Under("tasks"))
		p.View("Board", plugin.At("/board"))
		p.Command("mine", "Issues assigned to you", func(*plugin.Call) error { return nil }).Args("[PROJECT]")
		p.Sync("issues", 5*time.Minute, func(context.Context) error { return nil })
		p.Ref("issue", "Jira issue", func(context.Context, plugin.Asked) (plugin.Shown, error) { return plugin.Shown{}, nil }).
			Prefix("RAM-", "RAP-").Address("https://acme.atlassian.net/browse/{ref}").Opens("/issue/{ref}").Icon("ticket")
		p.Ref("sprint", "", func(context.Context, plugin.Asked) (plugin.Shown, error) { return plugin.Shown{}, nil }).Prefix("SPRINT-")
		p.Guide("", "crew", "orchestrator")
	}, plugintest.Name("jira"), plugintest.Config(map[string]any{"site": "https://x", "token": "t"}))

	got, _ := json.Marshal(ws.Registration())
	want := `{"description":"Jira issues beside your missions",` +
		`"views":[{"title":"Jira","icon":"ticket","under":"tasks","path":"/"},{"title":"Board","path":"/board"}],` +
		`"ui":{"script":"` + hashed("app.js", appJS) + `","style":"` + hashed("app.css", appCSS) + `"},` +
		`"commands":{"name":"jira","short":"Jira issues beside your missions","subcommands":[` +
		`{"name":"mine","short":"Issues assigned to you","args":"[PROJECT]"},` +
		`{"name":"sync","short":"Fetch what's new now","args":"[NAME...]"}]},` +
		`"agent":{"roles":["crew","orchestrator"],"guide":"Use ` + "`ws jira mine`" + `.\n"},` +
		`"syncs":[{"name":"issues","every":300}],` +
		`"refs":[{"kind":"issue","title":"Jira issue","icon":"ticket","prefixes":["RAM-","RAP-"],` +
		`"address":"https://acme.atlassian.net/browse/{ref}","path":"/issue/{ref}"},` +
		`{"kind":"sprint","prefixes":["SPRINT-"]}],` +
		`"config_version":2,` +
		`"config":[` +
		`{"key":"site","title":"Jira site","type":"url","required":true,"example":"https://acme.atlassian.net"},` +
		`{"key":"token","title":"API token","type":"secret","required":true,"help":"From id.atlassian.com","scope":"local"},` +
		`{"key":"projects","title":"Projects to show","type":"list","default":[]},` +
		`{"key":"kind","title":"Kind","type":"enum","default":"a","options":["a","b"]},` +
		`{"key":"email","title":"Your email","type":"string","scope":"local"}]}`
	if string(got) != want {
		t.Errorf("registration\n got %s\nwant %s", got, want)
	}
}

func TestAPluginThatDeclaresWronglyDoesNotStart(t *testing.T) {
	built := fstest.MapFS{"ui/dist/app.js": file(appJS)}
	ok := func(http.ResponseWriter, *http.Request) {}
	shows := func(context.Context, plugin.Asked) (plugin.Shown, error) { return plugin.Shown{}, nil }
	for name, tc := range map[string]struct {
		files fstest.MapFS
		setup func(p *plugin.Plugin)
		why   string // what the plugin's author is told
	}{
		"a view and no app": {
			setup: func(p *plugin.Plugin) { p.View("X") },
			why:   `the view "X" has no app to show`,
		},
		"an app that isn't built": {
			files: fstest.MapFS{"ui/dist/app.css": file(appCSS)},
			setup: func(p *plugin.Plugin) { p.UI("ui/dist"); p.View("X") },
			why:   "ui/dist/app.js, isn't in the plugin's files",
		},
		"an app and no files": {
			setup: func(p *plugin.Plugin) { p.UI("ui/dist") },
			why:   "ui/dist/app.js, isn't in the plugin's files",
		},
		"two apps": {
			files: built,
			setup: func(p *plugin.Plugin) { p.UI("ui/dist"); p.UI("ui/dist") },
			why:   "a plugin has one app",
		},
		"a view at a path that isn't one": {
			files: built,
			setup: func(p *plugin.Plugin) { p.UI("ui/dist"); p.View("X", plugin.At("board")) },
			why:   `the view "X" opens at "board"`,
		},
		"two handlers on one path": {
			setup: func(p *plugin.Plugin) { p.HandleFunc("GET /api/a", ok); p.HandleFunc("GET /api/a", ok) },
			why:   "the plugin's HTTP",
		},
		"a command declared twice": {
			setup: func(p *plugin.Plugin) {
				p.Command("a", "", func(*plugin.Call) error { return nil })
				p.Command("a", "", func(*plugin.Call) error { return nil })
			},
			why: "declared twice",
		},
		"a ref declared twice": {
			setup: func(p *plugin.Plugin) {
				p.Ref("issue", "", shows).Prefix("RAM-")
				p.Ref("issue", "", shows).Prefix("RAP-")
			},
			why: "declared twice",
		},
		"a ref that names nothing": {
			setup: func(p *plugin.Plugin) { p.Ref("issue", "", shows) },
			why:   "it has no prefixes",
		},
		"a ref whose prefix is ws's own kind of name": {
			setup: func(p *plugin.Plugin) { p.Ref("issue", "", shows).Prefix("demo#") },
			why:   `its prefix "demo#"`,
		},
		"a ref with nothing to show one": {
			setup: func(p *plugin.Plugin) { p.Ref("issue", "", nil).Prefix("RAM-") },
			why:   "nothing to show one",
		},
		"a ref whose address has nowhere for the name": {
			setup: func(p *plugin.Plugin) {
				p.Ref("issue", "", shows).Prefix("RAM-").Address("https://acme.atlassian.net/browse/")
			},
			why: "its address",
		},
		"a ref that opens at a path that isn't one": {
			setup: func(p *plugin.Plugin) { p.Ref("issue", "", shows).Prefix("RAM-").Opens("issue/{ref}") },
			why:   "its path",
		},
		"a config key declared twice": {
			setup: func(p *plugin.Plugin) {
				p.Config.String("a", "A")
				p.Config.Bool("a", "A")
			},
			why: "declared twice",
		},
	} {
		files := tc.files
		if files == nil {
			files = fstest.MapFS{}
		}
		ws, err := plugintest.Start(files, tc.setup)
		if err == nil {
			ws.Close()
			t.Errorf("a plugin with %s started", name)
		} else if !strings.Contains(err.Error(), tc.why) {
			t.Errorf("a plugin with %s didn't start, and didn't say why (%q): %v", name, tc.why, err)
		}
	}
}

// --- config ---

func TestConfigIsReadThroughItsKeys(t *testing.T) {
	var (
		site     *plugin.StringKey
		projects *plugin.ListKey
		closed   *plugin.BoolKey
		limit    *plugin.NumberKey
		changes  atomic.Int32
	)
	ws := plugintest.New(t, nil, func(p *plugin.Plugin) {
		site = p.Config.URL("site", "Site")
		projects = p.Config.List("projects", "Projects", plugin.Default([]string{"ALL"}))
		closed = p.Config.Bool("closed", "Show closed", plugin.Default(true))
		limit = p.Config.Number("limit", "Limit", plugin.Default(50))
		p.OnConfig(func() { changes.Add(1) })
	}, plugintest.Config(map[string]any{"site": "https://one"}))

	if site.Get() != "https://one" || !site.IsSet() {
		t.Errorf("site is %q", site.Get())
	}
	if got := projects.Get(); len(got) != 1 || got[0] != "ALL" || projects.IsSet() {
		t.Errorf("projects, not set, is %v", got)
	}
	if !closed.Get() || limit.Get() != 50 {
		t.Errorf("defaults: closed %v, limit %v", closed.Get(), limit.Get())
	}

	if err := ws.SetConfig(map[string]any{"site": "https://two", "projects": []string{"A", "B"}, "closed": false, "limit": 5}); err != nil {
		t.Fatal(err)
	}
	if site.Get() != "https://two" || len(projects.Get()) != 2 || closed.Get() || limit.Get() != 5 {
		t.Errorf("after a change: %q %v %v %v", site.Get(), projects.Get(), closed.Get(), limit.Get())
	}
	if changes.Load() != 1 {
		t.Errorf("OnConfig ran %d times", changes.Load())
	}
	select {
	case <-ws.Done():
		t.Error("the plugin stopped on a change to config no Setup read")
	default:
	}
}

// A plugin whose registration follows its config exits when that config
// changes, to be started afresh.
func TestAChangeToWhatSetupReadRestarts(t *testing.T) {
	ws := plugintest.New(t, nil, func(p *plugin.Plugin) {
		projects := p.Config.List("projects", "Projects")
		p.Config.String("other", "Other")
		p.Setup(func() {
			for _, name := range projects.Get() {
				p.Command(strings.ToLower(name), "Issues of "+name, func(*plugin.Call) error { return nil })
			}
		})
	}, plugintest.Config(map[string]any{"projects": []string{"PROJ", "OPS"}}))

	if cmds := ws.Registration().Commands; cmds == nil || len(cmds.Subcommands) != 2 || cmds.Subcommands[1].Name != "ops" {
		t.Fatalf("commands declared in Setup: %+v", cmds)
	}
	ws.SetConfig(map[string]any{"projects": []string{"PROJ", "OPS"}, "other": "x"}) //nolint:errcheck // values a test wrote
	select {
	case <-ws.Done():
		t.Fatal("the plugin stopped though what Setup read hadn't changed")
	case <-time.After(50 * time.Millisecond):
	}
	ws.SetConfig(map[string]any{"projects": []string{"PROJ"}}) //nolint:errcheck // values a test wrote
	select {
	case <-ws.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("the plugin carried on though what Setup read had changed")
	}
}

// --- commands ---

type issue struct {
	Key     string `json:"key" ws:"Issue"`
	Summary string `json:"summary"`
}

func (i issue) Markdown() string { return "## " + i.Key + "\n\n" + i.Summary }

func commands(p *plugin.Plugin) {
	issues := []issue{{"PROJ-1", "Token cache"}, {"PROJ-2", "Billing is slow"}}
	p.Describe("Issues")
	p.Command("mine", "Issues assigned to you", func(c *plugin.Call) error {
		if c.Flag("none") {
			return c.List([]issue{})
		}
		if project := c.Option("project"); project != "" {
			return c.Say("only " + project + " " + strings.Join(c.Args(), ","))
		}
		return c.List(issues)
	}).Flag("none", "Show nothing").Option("project", "Only this project").Args("[QUERY]")
	p.Command("show", "One issue", func(c *plugin.Call) error {
		if c.Arg(0) == "" {
			return plugin.Exit(3, errors.New("which issue?"))
		}
		return c.Show(issues[0])
	}).Args("KEY")
	p.Command("fail", "Fails", func(*plugin.Call) error { return errors.New("it broke") })
	p.Command("panic", "Panics", func(*plugin.Call) error { panic("oh no") })
	p.Command("wait", "Waits to be stopped", func(c *plugin.Call) error {
		<-c.Context().Done()
		return c.Context().Err()
	})
}

func TestCommands(t *testing.T) {
	ws := plugintest.New(t, nil, commands, plugintest.Name("jira"))

	for _, tc := range []struct {
		args   []string
		stdout string
		stderr string
		exit   int
	}{
		{args: []string{"mine"}, stdout: "ISSUE   SUMMARY\nPROJ-1  Token cache\nPROJ-2  Billing is slow\n"},
		{args: []string{"mine", "-f", "llm"}, stdout: "| Issue | Summary |\n| --- | --- |\n| PROJ-1 | Token cache |\n| PROJ-2 | Billing is slow |\n"},
		{args: []string{"mine", "--format=json"}, stdout: "[\n  {\n    \"key\": \"PROJ-1\",\n    \"summary\": \"Token cache\"\n  },\n  {\n    \"key\": \"PROJ-2\",\n    \"summary\": \"Billing is slow\"\n  }\n]\n"},
		{args: []string{"mine", "--none"}, stdout: "Nothing to show.\n"},
		{args: []string{"mine", "--none", "-f", "json"}, stdout: "[]\n"},
		{args: []string{"mine", "--project", "OPS", "a", "--", "-b"}, stdout: "only OPS a,-b\n"},
		{args: []string{"mine", "--project=OPS"}, stdout: "only OPS \n"},
		{args: []string{"show", "PROJ-1"}, stdout: "Issue:    PROJ-1\nSummary:  Token cache\n"},
		{args: []string{"show", "PROJ-1", "-f", "llm"}, stdout: "## PROJ-1\n\nToken cache\n"},
		{args: []string{"show"}, stderr: "which issue?\n", exit: 3},
		{args: []string{"fail"}, stderr: "it broke\n", exit: 1},
		{args: []string{"panic"}, stderr: "panic panicked: oh no\n", exit: 1},
	} {
		got := ws.Run(tc.args...)
		if got.Stdout != tc.stdout || got.Stderr != tc.stderr || got.Exit != tc.exit {
			t.Errorf("ws jira %s\n got %+v\nwant stdout %q stderr %q exit %d", strings.Join(tc.args, " "), got, tc.stdout, tc.stderr, tc.exit)
		}
	}
}

func TestCommandsHelpAndMistakes(t *testing.T) {
	ws := plugintest.New(t, nil, commands, plugintest.Name("jira"))

	help := ws.Run()
	if help.Exit != 0 || !strings.Contains(help.Stdout, "ws jira <command> [flags]") || !strings.Contains(help.Stdout, "mine   Issues assigned to you") {
		t.Errorf("no arguments: %+v", help)
	}
	if got := ws.Run("--help"); got.Stdout != help.Stdout {
		t.Errorf("--help differs from no arguments: %+v", got)
	}
	one := ws.Run("mine", "--help")
	for _, want := range []string{"ws jira mine [QUERY] [flags]", "--none", "Show nothing", "--project <value>", "-f, --format <format>"} {
		if !strings.Contains(one.Stdout, want) {
			t.Errorf("mine --help lacks %q:\n%s", want, one.Stdout)
		}
	}
	for _, args := range [][]string{{"nope"}, {"mine", "--nope"}, {"mine", "-f", "yaml"}, {"mine", "--project"}, {"mine", "--none=1"}} {
		if got := ws.Run(args...); got.Exit != 2 || got.Stdout != "" || got.Stderr == "" {
			t.Errorf("ws jira %s: %+v", strings.Join(args, " "), got)
		}
	}
}

func TestATableIsCutToTheTerminal(t *testing.T) {
	ws := plugintest.New(t, nil, func(p *plugin.Plugin) {
		p.Command("long", "", func(c *plugin.Call) error {
			return c.List([]issue{{"PROJ-1", strings.Repeat("word ", 40)}})
		})
	})
	ws.Terminal = protocol.Terminal{TTY: true, Width: 30}
	for _, line := range strings.Split(strings.TrimSpace(ws.Run("long").Stdout), "\n") {
		if n := len([]rune(line)); n > 30 {
			t.Errorf("a line %d wide in a terminal of 30: %q", n, line)
		}
	}
}

func TestACommandOutOfTimeSaysSo(t *testing.T) {
	ws := plugintest.New(t, nil, func(p *plugin.Plugin) {
		p.CommandTimeout = 20 * time.Millisecond
		commands(p)
	})
	got := ws.Run("wait")
	if got.Exit != 1 || !strings.Contains(got.Stderr, "ran out of time") {
		t.Errorf("a command past its time: %+v", got)
	}
}

// --- the view ---

func viewFiles() fstest.MapFS {
	return fstest.MapFS{
		"ui/dist/app.js":        file(appJS),
		"ui/dist/app.css":       file(appCSS),
		"ui/dist/chunks/one.js": file(`export const one = 1`),
		"ui/dist/logo.svg":      file(`<svg xmlns="http://www.w3.org/2000/svg"/>`),
		"ui/src/main.ts":        file(`the app's source: not served`),
		"agents.md":             file("not served either"),
	}
}

func view(saved *[]string) func(p *plugin.Plugin) {
	return func(p *plugin.Plugin) {
		p.UI("ui/dist")
		p.View("Jira", plugin.Icon("ticket"))
		p.View("Board", plugin.At("/board"))
		p.HandleFunc("GET /api/ping", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("pong")) })
		p.HandleFunc("GET /api/issues/{key}", func(w http.ResponseWriter, r *http.Request) {
			if r.PathValue("key") == "BAD" {
				panic("no such issue")
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"key": r.PathValue("key")}) //nolint:errcheck // a test's recorder
		})
		p.Handle("POST /api/save", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*saved = append(*saved, r.FormValue("name"))
			w.WriteHeader(http.StatusNoContent)
		}))
	}
}

// A plugin's HTTP is its app's files and what its app asks it, and
// nothing else: it serves no page.
func TestTheAppsFilesAndItsAPI(t *testing.T) {
	var saved []string
	ws := plugintest.New(t, viewFiles(), view(&saved), plugintest.Name("jira"))

	ui := ws.Registration().UI
	if ui == nil || ui.Script != hashed("app.js", appJS) || ui.Style != hashed("app.css", appCSS) {
		t.Fatalf("the app as registered: %+v", ui)
	}

	// The app's files, as what they are, where the registration says.
	for _, tc := range []struct{ path, kind, cache, body string }{
		{ui.Script, "text/javascript; charset=utf-8", "public, max-age=31536000, immutable", appJS},
		{ui.Style, "text/css; charset=utf-8", "public, max-age=31536000, immutable", appCSS},
		{"/ui/app.js", "text/javascript; charset=utf-8", "no-cache", appJS},
		{"/ui/chunks/one.js", "text/javascript; charset=utf-8", "no-cache", `export const one = 1`},
		{"/ui/logo.svg", "image/svg+xml", "no-cache", `<svg xmlns="http://www.w3.org/2000/svg"/>`},
	} {
		got := ws.Get(tc.path)
		if got.Status != 200 || got.Header.Get("Content-Type") != tc.kind || got.Header.Get("Cache-Control") != tc.cache || got.Body != tc.body {
			t.Errorf("GET %s: %d, %q, %q\n%s", tc.path, got.Status, got.Header.Get("Content-Type"), got.Header.Get("Cache-Control"), got.Body)
		}
	}

	// Nothing else is there, and nothing is HTML: not a view's path (ws
	// serves the page), not a folder of the app's, not the plugin's other
	// files.
	for _, path := range []string{"/", "/board", "/browse/PROJ-1", "/ui/", "/ui", "/ui/chunks/", "/ui/nope.js", "/ui/../agents.md", "/ui/../src/main.ts", "/agents.md", "/static/app.js"} {
		got := ws.Get(path)
		if got.Status != 404 || strings.Contains(got.Header.Get("Content-Type"), "html") || strings.Contains(got.Body, "<") {
			t.Errorf("GET %s: %d, %q\n%s", path, got.Status, got.Header.Get("Content-Type"), got.Body)
		}
	}

	// What the app asks the plugin.
	if ping := ws.Get("/api/ping"); ping.Status != 200 || ping.Body != "pong" {
		t.Errorf("a handler: %d %q", ping.Status, ping.Body)
	}
	if one := ws.Get("/api/issues/PROJ-1"); one.Status != 200 || strings.TrimSpace(one.Body) != `{"key":"PROJ-1"}` {
		t.Errorf("a handler with a path's value: %d %q", one.Status, one.Body)
	}
	if post := ws.Post("/api/save", map[string][]string{"name": {"x"}}); post.Status != 204 || len(saved) != 1 || saved[0] != "x" {
		t.Errorf("a POST: %d, saved %v", post.Status, saved)
	}
	if wrong := ws.Post("/api/ping", nil); wrong.Status != 405 {
		t.Errorf("a POST to a GET's handler: %d", wrong.Status)
	}

	// A handler that panics answers 500 in plain text, and ws is told.
	bad := ws.Get("/api/issues/BAD")
	if bad.Status != 500 || !strings.Contains(bad.Body, "no such issue") || !strings.HasPrefix(bad.Header.Get("Content-Type"), "text/plain") {
		t.Errorf("a handler that panicked: %d, %q\n%s", bad.Status, bad.Header.Get("Content-Type"), bad.Body)
	}
	if logs := ws.Logs(); len(logs) != 1 || logs[0].Level != "error" || !strings.Contains(logs[0].Message, "GET /api/issues/{key} panicked: no such issue") {
		t.Errorf("ws wasn't told of the panic: %+v", logs)
	}
}

// An app needs no stylesheet, and a plugin needs no app: one with only
// handlers serves them, and one with nothing serves nothing.
func TestAnAppWithNoStylesheetAndAPluginWithNoApp(t *testing.T) {
	bare := plugintest.New(t, fstest.MapFS{"web/app.js": file(appJS)}, func(p *plugin.Plugin) {
		p.UI("web/")
		p.View("X")
	})
	if ui := bare.Registration().UI; ui == nil || ui.Script != hashed("app.js", appJS) || ui.Style != "" {
		t.Errorf("an app with no stylesheet: %+v", ui)
	}
	if got := bare.Get("/ui/app.css"); got.Status != 404 {
		t.Errorf("a stylesheet that isn't there: %d", got.Status)
	}

	api := plugintest.New(t, nil, func(p *plugin.Plugin) {
		p.HandleFunc("POST /hooks/push", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	})
	if reg := api.Registration(); reg.UI != nil || len(reg.Views) != 0 {
		t.Errorf("a plugin with no app registered one: %+v", reg)
	}
	if got := api.Post("/hooks/push", nil); got.Status != 202 {
		t.Errorf("a handler of a plugin with no app: %d", got.Status)
	}

	nothing := plugintest.New(t, nil, func(*plugin.Plugin) {})
	if got := nothing.Get("/"); got.Status != 404 {
		t.Errorf("a plugin that serves nothing: %d", got.Status)
	}
}

func TestChangedAndBadgeTellWS(t *testing.T) {
	ws := plugintest.New(t, nil, func(p *plugin.Plugin) {
		p.Command("poke", "", func(*plugin.Call) error {
			p.Changed()
			p.Badge("/", 3)
			return nil
		})
	})
	ws.Run("poke")
	if n := len(ws.Notifications(protocol.MethodViewChanged)); n != 1 {
		t.Errorf("%d view/changed", n)
	}
	if got := ws.Notifications(protocol.MethodViewBadge); len(got) != 1 || string(got[0]) != `{"path":"/","count":3}` {
		t.Errorf("view/badge: %s", got)
	}
}

// --- the database ---

func visits(p *plugin.Plugin) {
	p.Command("visit", "", func(c *plugin.Call) error {
		if _, err := p.DB().Exec(`insert into visits (at, who) values (1, 'me')`); err != nil {
			return err
		}
		var n int
		if err := p.DB().QueryRow(`select count(*) from visits`).Scan(&n); err != nil {
			return err
		}
		return c.Say(strings.Repeat("x", n))
	})
	p.UI("ui/dist")
	p.View("V")
	p.HandleFunc("GET /api/visits", func(w http.ResponseWriter, r *http.Request) {
		var n int
		if err := p.DB().QueryRowContext(r.Context(), `select count(*) from visits`).Scan(&n); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]int{"visits": n}) //nolint:errcheck // a test's recorder
	})
}

func TestMigrationsAreAppliedInOrderEachOnce(t *testing.T) {
	files := fstest.MapFS{
		"ui/dist/app.js":             file(appJS),
		"migrations/0001_visits.sql": file("create table visits (at integer not null);"),
		"migrations/0002_who.sql":    file("alter table visits add column who text;\ncreate index visits_who on visits (who);"),
		"migrations/notes.txt":       file("not a migration"),
	}
	ws := plugintest.New(t, files, visits)
	if got := ws.Run("visit"); got.Stdout != "x\n" {
		t.Fatalf("after both migrations: %+v", got)
	}
	if got := ws.Run("visit"); got.Stdout != "xx\n" {
		t.Fatalf("a second visit: %+v", got)
	}
	if got := ws.Get("/api/visits"); got.Status != 200 || strings.TrimSpace(got.Body) != `{"visits":2}` {
		t.Errorf("the app's API, reading the database: %d %q", got.Status, got.Body)
	}
	if _, err := os.Stat(filepath.Join(ws.Data(), "plugin.db")); err != nil {
		t.Errorf("the database isn't in the data folder: %v", err)
	}
}

func TestAFailedMigrationStopsThePluginRunning(t *testing.T) {
	files := fstest.MapFS{
		"ui/dist/app.js":             file(appJS),
		"migrations/0001_visits.sql": file("create table visits (at integer not null);"),
		"migrations/0002_bad.sql":    file("create table other (x integer);\nalter table nowhere add column y;"),
	}
	ws := plugintest.New(t, files, visits)

	// It still registered: ws can show what it is, and what's wrong.
	if len(ws.Registration().Views) != 1 {
		t.Errorf("a plugin with a bad migration didn't register its view")
	}
	if logs := ws.Logs(); len(logs) != 1 || logs[0].Level != "error" || !strings.Contains(logs[0].Message, "0002_bad.sql") {
		t.Errorf("ws wasn't told which migration failed: %+v", logs)
	}
	if got := ws.Run("visit"); got.Exit != 1 || !strings.Contains(got.Stderr, "0002_bad.sql") {
		t.Errorf("a command ran against a half-migrated database: %+v", got)
	}
	// The app still loads, and what it asks is answered with why not, in
	// plain text.
	if app := ws.Get(ws.Registration().UI.Script); app.Status != 200 {
		t.Errorf("the app of a plugin with a bad migration: %d", app.Status)
	}
	if got := ws.Get("/api/visits"); got.Status != 503 || !strings.Contains(got.Body, "0002_bad.sql") || !strings.HasPrefix(got.Header.Get("Content-Type"), "text/plain") {
		t.Errorf("a handler ran against a half-migrated database: %d, %q\n%s", got.Status, got.Header.Get("Content-Type"), got.Body)
	}
}

// A database with something in it is copied aside before it's changed.
func TestTheDatabaseIsCopiedAsideBeforeAMigration(t *testing.T) {
	dir := t.TempDir()
	first := fstest.MapFS{
		"ui/dist/app.js":             file(appJS),
		"migrations/0001_visits.sql": file("create table visits (at integer not null, who text);"),
	}
	ws := plugintest.New(t, first, visits, plugintest.Dir(dir))
	ws.Run("visit")
	ws.Close()
	if _, err := os.Stat(filepath.Join(dir, "data", "plugin.db.bak")); err == nil {
		t.Fatal("a new database was copied aside: there was nothing to keep")
	}

	second := fstest.MapFS{
		"ui/dist/app.js":             file(appJS),
		"migrations/0001_visits.sql": first["migrations/0001_visits.sql"],
		"migrations/0002_index.sql":  file("create index visits_who on visits (who);"),
	}
	ws = plugintest.New(t, second, visits, plugintest.Dir(dir))
	if got := ws.Run("visit"); got.Stdout != "xx\n" {
		t.Errorf("what was kept didn't survive a migration: %+v", got)
	}
	if info, err := os.Stat(filepath.Join(dir, "data", "plugin.db.bak")); err != nil || info.Size() == 0 {
		t.Errorf("the database wasn't copied aside before the migration: %v", err)
	}
}

// --- sync ---

func TestSyncRunsAtStartAndOnDemandOneAtATime(t *testing.T) {
	var runs atomic.Int32
	release := make(chan struct{})
	ws := plugintest.New(t, nil, func(p *plugin.Plugin) {
		p.Sync("issues", time.Hour, func(context.Context) error {
			if runs.Add(1) > 1 {
				<-release
			}
			return nil
		})
	})

	// At start, unasked.
	states := ws.Wait(protocol.MethodSyncState, 1)
	if len(states) != 1 || runs.Load() != 1 {
		t.Fatalf("at start: %d runs, %s", runs.Load(), states)
	}
	var st protocol.SyncStateParams
	json.Unmarshal(states[0], &st) //nolint:errcheck // the plugin's own JSON
	if st.Name != "issues" || st.Error != "" || st.At == "" {
		t.Errorf("sync/state: %+v", st)
	}
	if n := len(ws.Wait(protocol.MethodViewChanged, 1)); n != 1 {
		t.Errorf("the open app wasn't told after a sync: %d view/changed", n)
	}
	if states := ws.Plugin().SyncStates(); len(states) != 1 || states[0].Name != "issues" || states[0].At.IsZero() || states[0].Error != "" {
		t.Errorf("how the sync stands, for a plugin's app to show: %+v", states)
	}

	// Asked for three times at once: one run, three answers.
	var wg sync.WaitGroup
	results := make([]plugintest.Result, 3)
	for i := range results {
		wg.Add(1)
		go func() { defer wg.Done(); results[i] = ws.Run("sync") }()
	}
	for runs.Load() < 2 {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // the other two have asked by now
	close(release)
	wg.Wait()
	if runs.Load() != 2 {
		t.Errorf("three requests at once ran the sync %d times, not once", runs.Load()-1)
	}
	for _, r := range results {
		if r.Exit != 0 || !strings.Contains(r.Stdout, "issues") {
			t.Errorf("ws x sync: %+v", r)
		}
	}

	// Asked for by the plugin's own code: a handler its app posts to, say.
	if err := ws.Plugin().SyncNow(context.Background()); err != nil || runs.Load() != 3 {
		t.Errorf("SyncNow: %v, %d runs", err, runs.Load())
	}
}

func TestAFailedSyncIsRetriedSoonerAndSaysSo(t *testing.T) {
	defer plugin.SetRetryAfter(10 * time.Millisecond)()
	var runs atomic.Int32
	ws := plugintest.New(t, nil, func(p *plugin.Plugin) {
		p.Sync("issues", time.Hour, func(context.Context) error {
			if runs.Add(1) < 3 {
				return errors.New("401 from Jira")
			}
			return nil
		})
	})
	states := ws.Wait(protocol.MethodSyncState, 3)
	if len(states) != 3 {
		t.Fatalf("a failing sync was run %d times in five seconds, with an hour between syncs", len(states))
	}
	var first, third protocol.SyncStateParams
	json.Unmarshal(states[0], &first) //nolint:errcheck // the plugin's own JSON
	json.Unmarshal(states[2], &third) //nolint:errcheck // the plugin's own JSON
	if first.Error != "401 from Jira" || third.Error != "" {
		t.Errorf("first %+v, third %+v", first, third)
	}
	if logs := ws.Logs(); len(logs) != 2 || logs[0].Level != "warn" || !strings.Contains(logs[0].Message, "401 from Jira") {
		t.Errorf("ws wasn't told of the failures: %+v", logs)
	}
	if got := ws.Run("sync", "nope"); got.Exit != 2 {
		t.Errorf("a sync that doesn't exist: %+v", got)
	}
}

func TestSyncWaitsForRequiredConfig(t *testing.T) {
	var runs atomic.Int32
	ws := plugintest.New(t, nil, func(p *plugin.Plugin) {
		p.Config.Secret("token", "Token", plugin.Required)
		p.Sync("issues", time.Hour, func(context.Context) error { runs.Add(1); return nil })
	})
	time.Sleep(50 * time.Millisecond)
	if runs.Load() != 0 {
		t.Fatalf("a sync ran with required config missing")
	}
	if got := ws.Run("sync"); got.Exit != 1 || !strings.Contains(got.Stderr, "config isn't complete") {
		t.Errorf("ws x sync with config missing: %+v", got)
	}
	ws.SetConfig(map[string]any{"token": "t"}) //nolint:errcheck // values a test wrote
	if n := len(ws.Wait(protocol.MethodSyncState, 1)); n != 1 || runs.Load() != 1 {
		t.Errorf("once config was set: %d runs", runs.Load())
	}
}

// --- declaring ---

func TestNothingIsDeclaredOnceRunning(t *testing.T) {
	ws := plugintest.New(t, nil, func(p *plugin.Plugin) {
		p.Command("late", "", func(c *plugin.Call) error {
			p.Command("another", "", func(*plugin.Call) error { return nil })
			return nil
		})
	})
	if got := ws.Run("late"); got.Exit != 1 || !strings.Contains(got.Stderr, "after Run has started") {
		t.Errorf("a command declared while running: %+v", got)
	}
}

// --- asking ws ---

func TestAskingWS(t *testing.T) {
	ws := plugintest.New(t, nil, func(p *plugin.Plugin) {
		p.Command("missions", "", func(c *plugin.Call) error {
			ms, err := p.Missions(c.Context(), c.Flag("all"))
			if err != nil {
				return err
			}
			for _, m := range ms {
				fmt.Fprintln(c.Stdout(), m.Slug, m.State, m.Headline.Court)
			}
			return nil
		}).Flag("all", "archived too")
		p.Command("mission", "", func(c *plugin.Call) error {
			m, err := p.Mission(c.Context(), c.Arg(0))
			if errors.Is(err, plugin.ErrNotFound) {
				return c.Say("none")
			}
			if err != nil {
				return err
			}
			return c.Say(m.Title + ": " + m.Brief + " in " + m.Capsules[0].PullRequest.State)
		}).Args("SLUG")
		p.Command("repos", "", func(c *plugin.Call) error {
			rs, err := p.Repos(c.Context())
			if err != nil {
				return err
			}
			for _, r := range rs {
				fmt.Fprintln(c.Stdout(), r.Name, len(r.Capsules), r.Capsules[0].Mission)
			}
			return nil
		})
	})

	// A ws from before these: each is ErrUnsupported, saying so.
	if got := ws.Run("repos"); got.Exit == 0 || !strings.Contains(got.Stderr, "update ws") {
		t.Errorf("repos of a ws that can't answer: %+v", got)
	}

	ws.Missions(
		protocol.MissionDetail{
			Mission: protocol.Mission{Slug: "token-cache", Title: "Token cache", State: protocol.MissionReview,
				Headline: &protocol.Headline{Court: protocol.CourtYou, Text: "PR #12 waits on your review"},
				Capsules: []protocol.MissionCapsule{{Repo: "api", Name: "token-cache", PullRequest: &protocol.PullRequest{Number: 12, State: "open"}}}},
			Brief: "Cache tokens",
		},
		protocol.MissionDetail{Mission: protocol.Mission{Slug: "old", State: protocol.MissionArchived, Headline: &protocol.Headline{Court: protocol.CourtAgent}}},
	)
	ws.Repos(protocol.Repo{Name: "api", Capsules: []protocol.Capsule{{Name: "token-cache", Mission: "token-cache"}}})

	if got := ws.Run("missions"); got.Stdout != "token-cache review you\n" {
		t.Errorf("missions in play: %+v", got)
	}
	if got := ws.Run("missions", "--all"); got.Stdout != "token-cache review you\nold archived agent\n" {
		t.Errorf("missions, archived too: %+v", got)
	}
	if got := ws.Run("mission", "token-cache"); got.Stdout != "Token cache: Cache tokens in open\n" {
		t.Errorf("a mission: %+v", got)
	}
	if got := ws.Run("mission", "nope"); got.Stdout != "none\n" {
		t.Errorf("a mission that isn't there: %+v", got)
	}
	if got := ws.Run("repos"); got.Stdout != "api 1 token-cache\n" {
		t.Errorf("repos: %+v", got)
	}
}

// --- the protocol's version ---

// --- refs ---

func TestWSAsksWhatANameIs(t *testing.T) {
	ws := plugintest.New(t, nil, func(p *plugin.Plugin) {
		p.Ref("issue", "Jira issue", func(_ context.Context, a plugin.Asked) (plugin.Shown, error) {
			switch a.Name {
			case "RAM-1":
				md := "The clock should be injected."
				if a.Full {
					md += "\n\n## Comments\n\nDana: agreed."
				}
				return plugin.Shown{Title: "Token refresh is flaky", State: "In Progress", Fields: []plugin.Field{{"Assignee", "Dana"}}, Markdown: md}, nil
			case "RAM-2":
				panic("the issue's gone wrong")
			}
			return plugin.Shown{}, plugin.ErrNotFound
		}).Prefix("RAM-")
	})

	got, err := ws.Show("issue", "RAM-1", false)
	want := protocol.RefShowResult{Title: "Token refresh is flaky", State: "In Progress", Fields: []protocol.RefField{{Label: "Assignee", Value: "Dana"}}, Markdown: "The clock should be injected."}
	if err != nil || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("RAM-1: %+v, %v; want %+v", got, err, want)
	}
	// Everything, when everything's asked for.
	if got, err := ws.Show("issue", "RAM-1", true); err != nil || !strings.Contains(got.Markdown, "Dana: agreed.") {
		t.Errorf("RAM-1 in full: %+v, %v", got, err)
	}
	// A name the plugin has nothing by is said to be, not failed on.
	if _, err := ws.Show("issue", "RAM-9", false); !errors.Is(err, plugin.ErrNotFound) || !strings.Contains(err.Error(), "there's no issue RAM-9") {
		t.Errorf("RAM-9: %v, want not found", err)
	}
	// A handler that panics fails its own answer, and the plugin runs on.
	if _, err := ws.Show("issue", "RAM-2", false); err == nil || !strings.Contains(err.Error(), "gone wrong") {
		t.Errorf("RAM-2: %v, want what went wrong", err)
	}
	if _, err := ws.Show("sprint", "SPRINT-1", false); err == nil || !strings.Contains(err.Error(), `no ref called "sprint"`) {
		t.Errorf("a kind that isn't registered: %v", err)
	}
	if _, err := ws.Show("issue", "RAM-1", false); err != nil {
		t.Errorf("after all that: %v", err)
	}
}

// What a ref's names follow is usually config: declared in Setup, a
// change to it starts the plugin afresh, and it registers them again.
func TestRefsFollowConfig(t *testing.T) {
	ws := plugintest.New(t, nil, func(p *plugin.Plugin) {
		projects := p.Config.List("projects", "Projects")
		p.Setup(func() {
			r := p.Ref("issue", "Issue", func(context.Context, plugin.Asked) (plugin.Shown, error) { return plugin.Shown{}, nil })
			for _, key := range projects.Get() {
				r.Prefix(key + "-")
			}
		})
	}, plugintest.Config(map[string]any{"projects": []string{"RAM", "RAP"}}))
	refs := ws.Registration().Refs
	if len(refs) != 1 || strings.Join(refs[0].Prefixes, " ") != "RAM- RAP-" {
		t.Errorf("refs: %+v", refs)
	}
}

func TestTheHandshakeChoosesTheHighestBothSpeak(t *testing.T) {
	declare := func(p *plugin.Plugin) { p.Command("hi", "", func(c *plugin.Call) error { return c.Say("hi") }) }
	if ws := plugintest.New(t, nil, declare); ws.Protocol() != protocol.Version {
		t.Errorf("chose %d, want %d", ws.Protocol(), protocol.Version)
	}
	// A ws newer than the plugin, which still speaks what it does.
	ws := plugintest.New(t, nil, declare, plugintest.Offer(1, 99))
	if ws.Protocol() != 1 {
		t.Errorf("a ws that speaks 1 and 99: chose %d", ws.Protocol())
	}
	if got := ws.Run("hi"); got.Exit != 0 || got.Stdout != "hi\n" {
		t.Errorf("a command after the handshake: %+v", got)
	}
}

// With none in common, the plugin registers nothing and says what it
// speaks, so ws can say which side is behind.
func TestNoVersionInCommonRegistersNothing(t *testing.T) {
	var ran atomic.Bool
	_, err := plugintest.Start(nil, func(p *plugin.Plugin) {
		p.Setup(func() { ran.Store(true) })
		p.Command("hi", "", func(*plugin.Call) error { return nil })
	}, plugintest.Offer(99))
	if err == nil || !strings.Contains(err.Error(), "version 0") || !strings.Contains(err.Error(), "[1]") || !strings.Contains(err.Error(), "[99]") {
		t.Fatalf("started against a ws that speaks only 99: %v", err)
	}
	if ran.Load() {
		t.Error("the plugin set itself up for a ws it can't speak to")
	}
}

// A ws from before versions were chosen sends protocol 1 and no list,
// and takes nothing but 1 in answer.
func TestAWSFromBeforeVersionsWereChosen(t *testing.T) {
	p := plugin.New(nil)
	ws, plug := io.Pipe()
	answers, out := io.Pipe()
	go p.Serve(ws, out) //nolint:errcheck // ended by closing its input
	defer plug.Close()
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocol":1,"ws":{"version":"0.3.0"},"plugin":{"name":"old","version":"0.0.0","dir":"` + t.TempDir() + `","data":"` + t.TempDir() + `"},"workspace":{"root":"/ws"},"config":{},"view":{"listen":"","base":"/plugins/old"}}}` + "\n"
	if _, err := plug.Write([]byte(init)); err != nil {
		t.Fatal(err)
	}
	var reply struct {
		Result protocol.InitializeResult `json:"result"`
		Error  json.RawMessage           `json:"error"`
	}
	if err := json.NewDecoder(answers).Decode(&reply); err != nil {
		t.Fatal(err)
	}
	if reply.Error != nil || reply.Result.Protocol != 1 {
		t.Errorf("answered %+v, error %s", reply.Result, reply.Error)
	}
}

// A command with a timeout of its own outlasts the plugin's, and one with
// none runs until it's stopped.
func TestCommandTimeout(t *testing.T) {
	ws := plugintest.New(t, nil, func(p *plugin.Plugin) {
		p.CommandTimeout = 20 * time.Millisecond
		wait := func(c *plugin.Call) error {
			select {
			case <-c.Context().Done():
				return c.Context().Err()
			case <-time.After(120 * time.Millisecond):
				return c.Say("done")
			}
		}
		p.Command("short", "Runs out of time", wait)
		p.Command("long", "Has longer", wait).Timeout(time.Second)
		p.Command("forever", "Has as long as it likes", wait).Timeout(0)
	})
	if got := ws.Run("short"); got.Exit != 1 || !strings.Contains(got.Stderr, "ran out of time") {
		t.Errorf("short: exit %d, %q", got.Exit, got.Stderr)
	}
	for _, name := range []string{"long", "forever"} {
		if got := ws.Run(name); got.Exit != 0 || got.Stdout != "done\n" {
			t.Errorf("%s: exit %d, %q %q", name, got.Exit, got.Stdout, got.Stderr)
		}
	}
}

// p.WS runs ws's own program in the workspace, and says what went wrong.
func TestWS(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(t.TempDir(), "ws")
	body := "#!/bin/sh\nif [ \"$1\" = fail ]; then echo partial; echo 'no such thing' >&2; exit 3; fi\necho \"$PWD|$WS_ORIGIN_PWD|$*\"\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	var p *plugin.Plugin
	plugintest.New(t, nil, func(pl *plugin.Plugin) { p = pl }, plugintest.Root(root), plugintest.Executable(script))

	out, err := p.WS(context.Background(), "show", "m12")
	if err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(root)
	if got := strings.TrimSpace(string(out)); got != real+"|"+root+"|show m12" && got != root+"|"+root+"|show m12" {
		t.Errorf("ws ran as %q", got)
	}
	out, err = p.WS(context.Background(), "fail")
	if err == nil || !strings.Contains(err.Error(), "no such thing") || string(out) != "partial\n" {
		t.Errorf("a failing command: %q, %v", out, err)
	}

	// With no program to run, it says so.
	var bare *plugin.Plugin
	plugintest.New(t, nil, func(pl *plugin.Plugin) { bare = pl })
	if _, err := bare.WS(context.Background(), "show"); !errors.Is(err, plugin.ErrNoWS) {
		t.Errorf("with no ws: %v", err)
	}
}
