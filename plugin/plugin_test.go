package plugin_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/brudil/workspace-kit/plugin"
	"github.com/brudil/workspace-kit/plugintest"
	"github.com/brudil/workspace-kit/protocol"
)

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

// --- registration ---

func TestRegistrationIsWhatWasDeclared(t *testing.T) {
	files := fstest.MapFS{
		"agents.md":        file("Use `ws jira mine`.\n"),
		"views/index.html": file("<h1>Issues</h1>"),
	}
	ws := plugintest.New(t, files, func(p *plugin.Plugin) {
		p.Describe("Jira issues beside your missions")
		p.Config.Version(2)
		p.Config.URL("site", "Jira site", plugin.Required, plugin.Example("https://acme.atlassian.net"))
		p.Config.Secret("token", "API token", plugin.Required, plugin.Help("From id.atlassian.com"))
		p.Config.List("projects", "Projects to show", plugin.Default([]string{}))
		p.Config.Enum("kind", "Kind", []string{"a", "b"}, plugin.Default("a"))
		p.Config.String("email", "Your email", plugin.Local)
		p.View("Jira", plugin.Icon("ticket"), plugin.Under("tasks"), func(v *plugin.View) {
			v.Page("/", "index.html", func(*plugin.Request) (any, error) { return nil, nil })
		})
		p.Command("mine", "Issues assigned to you", func(*plugin.Call) error { return nil }).Args("[PROJECT]")
		p.Sync("issues", 5*time.Minute, func(context.Context) error { return nil })
		p.Guide("", "crew", "orchestrator")
	}, plugintest.Name("jira"), plugintest.Config(map[string]any{"site": "https://x", "token": "t"}))

	got, _ := json.Marshal(ws.Registration())
	want := `{"description":"Jira issues beside your missions",` +
		`"views":[{"title":"Jira","icon":"ticket","under":"tasks","path":"/"}],` +
		`"commands":{"name":"jira","short":"Jira issues beside your missions","subcommands":[` +
		`{"name":"mine","short":"Issues assigned to you","args":"[PROJECT]"},` +
		`{"name":"sync","short":"Fetch what's new now","args":"[NAME...]"}]},` +
		`"agent":{"roles":["crew","orchestrator"],"guide":"Use ` + "`ws jira mine`" + `.\n"},` +
		`"syncs":[{"name":"issues","every":300}],` +
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
	for name, setup := range map[string]func(p *plugin.Plugin){
		"a template that isn't there": func(p *plugin.Plugin) {
			p.View("X", func(v *plugin.View) {
				v.Page("/", "missing.html", func(*plugin.Request) (any, error) { return nil, nil })
			})
		},
		"a command declared twice": func(p *plugin.Plugin) {
			p.Command("a", "", func(*plugin.Call) error { return nil })
			p.Command("a", "", func(*plugin.Call) error { return nil })
		},
		"a config key declared twice": func(p *plugin.Plugin) {
			p.Config.String("a", "A")
			p.Config.Bool("a", "A")
		},
	} {
		if ws, err := plugintest.Start(fstest.MapFS{}, setup); err == nil {
			ws.Close()
			t.Errorf("a plugin with %s started", name)
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
		"views/index.html": file(`<h1>{{.Greeting}}</h1><a href="{{url "/browse/PROJ-1"}}">one</a> {{pill "Done" "good"}} {{reltime .At}}`),
		"views/issue.html": file(`{{define "title"}}{{.}} Token cache{{end}}<h1>{{.}}</h1>`),
		"static/app.js":    file(`console.log("hi")`),
	}
}

func view(saved *[]string) func(p *plugin.Plugin) {
	return func(p *plugin.Plugin) {
		p.View("Jira", plugin.Icon("ticket"), func(v *plugin.View) {
			v.Page("/", "index.html", func(*plugin.Request) (any, error) {
				return map[string]any{"Greeting": "<Hello>", "At": time.Now().Add(-5 * time.Minute)}, nil
			})
			v.Page("/browse/{ref}", "issue.html", func(r *plugin.Request) (any, error) {
				if r.PathValue("ref") == "BAD" {
					return nil, errors.New("no such issue")
				}
				return r.PathValue("ref"), nil
			})
			v.Post("/save", func(r *plugin.Request) error {
				*saved = append(*saved, r.FormValue("name"))
				return nil
			})
			v.Handle("GET /api/ping", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("pong")) }))
		})
	}
}

func TestPages(t *testing.T) {
	var saved []string
	ws := plugintest.New(t, viewFiles(), view(&saved), plugintest.Name("jira"))

	page := ws.Get("/")
	for _, want := range []string{
		`<base href="/plugins/jira/">`,
		`<title>Jira</title>`,
		`<link rel="stylesheet" href="/plugin-kit.css">`,
		`<script src="/plugin-kit.js"></script>`,
		`<h1>&lt;Hello&gt;</h1>`, // escaped, as html/template does
		`<a href="/plugins/jira/browse/PROJ-1">one</a>`,
		`<span class="pill pill-good">Done</span>`,
		`5 minutes ago`,
	} {
		if !strings.Contains(page.Body, want) {
			t.Errorf("the page lacks %s:\n%s", want, page.Body)
		}
	}
	if page.Status != 200 {
		t.Errorf("the page's status is %d", page.Status)
	}

	issue := ws.Get("/browse/PROJ-1")
	if !strings.Contains(issue.Body, "<title>PROJ-1 Token cache</title>") || !strings.Contains(issue.Body, "<h1>PROJ-1</h1>") {
		t.Errorf("a page with a title of its own:\n%s", issue.Body)
	}

	if bad := ws.Get("/browse/BAD"); bad.Status != 500 || !strings.Contains(bad.Body, "no such issue") || !strings.Contains(bad.Body, "plugin-kit.css") {
		t.Errorf("a page whose handler failed: %d\n%s", bad.Status, bad.Body)
	}
	if missing := ws.Get("/nope"); missing.Status != 404 || !strings.Contains(missing.Body, "nothing here") {
		t.Errorf("a page that isn't there: %d\n%s", missing.Status, missing.Body)
	}
	if js := ws.Get("/static/app.js"); js.Status != 200 || js.Body != `console.log("hi")` {
		t.Errorf("a static file: %d %q", js.Status, js.Body)
	}
	if ping := ws.Get("/api/ping"); ping.Body != "pong" {
		t.Errorf("a handler of the plugin's own: %q", ping.Body)
	}

	post := ws.Post("/save", map[string][]string{"name": {"x"}, "next": {"/browse/PROJ-1"}})
	if post.Status != 303 || post.Header.Get("Location") != "/plugins/jira/browse/PROJ-1" || len(saved) != 1 || saved[0] != "x" {
		t.Errorf("a form: %d to %q, saved %v", post.Status, post.Header.Get("Location"), saved)
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
	p.View("V", func(v *plugin.View) {
		v.Page("/", "index.html", func(*plugin.Request) (any, error) { return nil, nil })
	})
}

func TestMigrationsAreAppliedInOrderEachOnce(t *testing.T) {
	files := fstest.MapFS{
		"views/index.html":           file("ok"),
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
	if _, err := os.Stat(filepath.Join(ws.Data(), "plugin.db")); err != nil {
		t.Errorf("the database isn't in the data folder: %v", err)
	}
}

func TestAFailedMigrationStopsThePluginRunning(t *testing.T) {
	files := fstest.MapFS{
		"views/index.html":           file("ok"),
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
	if page := ws.Get("/"); page.Status != 503 || !strings.Contains(page.Body, "0002_bad.sql") {
		t.Errorf("a page was served from a half-migrated database: %d\n%s", page.Status, page.Body)
	}
}

// A database with something in it is copied aside before it's changed.
func TestTheDatabaseIsCopiedAsideBeforeAMigration(t *testing.T) {
	dir := t.TempDir()
	first := fstest.MapFS{
		"views/index.html":           file("ok"),
		"migrations/0001_visits.sql": file("create table visits (at integer not null, who text);"),
	}
	ws := plugintest.New(t, first, visits, plugintest.Dir(dir))
	ws.Run("visit")
	ws.Close()
	if _, err := os.Stat(filepath.Join(dir, "data", "plugin.db.bak")); err == nil {
		t.Fatal("a new database was copied aside: there was nothing to keep")
	}

	second := fstest.MapFS{
		"views/index.html":           file("ok"),
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
	ws := plugintest.New(t, fstest.MapFS{"views/index.html": file("ok")}, func(p *plugin.Plugin) {
		p.Sync("issues", time.Hour, func(context.Context) error {
			if runs.Add(1) > 1 {
				<-release
			}
			return nil
		})
		p.View("V", func(v *plugin.View) {
			v.Page("/", "index.html", func(*plugin.Request) (any, error) { return nil, nil })
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
		t.Errorf("open pages weren't told after a sync: %d view/changed", n)
	}
	if page := ws.Get("/"); !strings.Contains(page.Body, "issues as of just now") || !strings.Contains(page.Body, `action="-/sync"`) {
		t.Errorf("the page doesn't say when it was synced:\n%s", page.Body)
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

	// Refresh on a page.
	if post := ws.Post("/-/sync", nil); post.Status != 303 || runs.Load() != 3 {
		t.Errorf("Refresh: %d, %d runs", post.Status, runs.Load())
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
