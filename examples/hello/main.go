// ws-hello is the smallest plugin worth reading: an app in ws's web UI,
// two commands, a config key, a table of its own, and names for what's
// in it.
package main

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/withworkspace/plugin"
)

// ui/dist is the app as built, from ui/. It's committed, so Go alone
// builds the plugin.
//
//go:embed agents.md migrations ui/dist
var files embed.FS

func main() {
	p := plugin.New(files)
	setup(p)
	p.Run()
}

// setup declares the plugin. It's apart from main so a test can declare
// the same plugin (see main_test.go).
func setup(p *plugin.Plugin) {
	p.Describe("Says hello")

	// Config: declared and read in one. Settings draws the field.
	greeting := p.Config.String("greeting", "Greeting", plugin.Default("Hello"))

	// visits counts what's in the table migrations/0001_visits.sql made.
	visits := func(ctx context.Context) (n int, err error) {
		err = p.DB().QueryRowContext(ctx, `select count(*) from visits`).Scan(&n)
		return n, err
	}

	// The app, and its entry in ws's sidebar. ws serves the page and loads
	// the app in it: the plugin has no HTML.
	p.UI("ui/dist")
	p.View("Hello", plugin.Icon("sparkle"))

	// What the app asks: the greeting and the count, as JSON. The app
	// fetches it at ws.api("/api/hello").
	hello := func(w http.ResponseWriter, r *http.Request) {
		n, err := visits(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"greeting": greeting.Get(), "visits": n}) //nolint:errcheck // the app has gone
	}
	p.HandleFunc("GET /api/hello", hello)

	// Each look at the app is counted: it posts here as it loads.
	p.HandleFunc("POST /api/visits", func(w http.ResponseWriter, r *http.Request) {
		if _, err := p.DB().ExecContext(r.Context(), `insert into visits values (unixepoch())`); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		hello(w, r)
	})

	// VISIT-3 is the third look at the app. ws finds a name like it
	// wherever it's written (a brief, a conversation), and asks here what
	// one is: for ws show VISIT-3, and the card under the name.
	p.Ref("visit", "Visit", func(ctx context.Context, a plugin.Asked) (plugin.Shown, error) {
		n, _ := strconv.Atoi(strings.TrimPrefix(a.Name, "VISIT-"))
		var at int64
		err := p.DB().QueryRowContext(ctx, `select at from visits order by rowid limit 1 offset ?`, n-1).Scan(&at)
		if errors.Is(err, sql.ErrNoRows) {
			return plugin.Shown{}, plugin.ErrNotFound
		}
		if err != nil {
			return plugin.Shown{}, err
		}
		when := time.Unix(at, 0).UTC().Format("2 January 2006 at 15:04 UTC")
		return plugin.Shown{
			Title:    "Visit " + strconv.Itoa(n),
			Fields:   []plugin.Field{{Label: "When", Value: when}},
			Markdown: "The app was looked at on " + when + ", and said: " + greeting.Get() + ".",
		}, nil
	}).Prefix("VISIT-")

	// ws hello say
	p.Command("say", "Says the greeting", func(c *plugin.Call) error {
		name := "workspace"
		if c.Arg(0) != "" {
			name = c.Arg(0)
		}
		return c.Say(greeting.Get() + ", " + name)
	}).Args("[NAME]")

	// ws hello visits: a line for a person, JSON or markdown when asked.
	p.Command("visits", "How often the app has been seen", func(c *plugin.Call) error {
		n, err := visits(c.Context())
		if err != nil {
			return err
		}
		return c.Show(struct {
			Visits int `json:"visits"`
		}{n})
	})
}
