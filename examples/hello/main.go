// ws-hello is the smallest plugin worth reading: a page, two commands, a
// config key and a table of its own.
package main

import (
	"context"
	"embed"

	"github.com/withworkspace/plugin"
)

//go:embed agents.md migrations views
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

	// The view: views/index.html, given what the function returns.
	p.View("Hello", plugin.Icon("sparkle"), func(v *plugin.View) {
		v.Page("/", "index.html", func(r *plugin.Request) (any, error) {
			if _, err := p.DB().ExecContext(r.Context(), `insert into visits values (unixepoch())`); err != nil {
				return nil, err
			}
			n, err := visits(r.Context())
			return map[string]any{"Greeting": greeting.Get(), "Visits": n}, err
		})
	})

	// ws hello say
	p.Command("say", "Says the greeting", func(c *plugin.Call) error {
		name := "workspace"
		if c.Arg(0) != "" {
			name = c.Arg(0)
		}
		return c.Say(greeting.Get() + ", " + name)
	}).Args("[NAME]")

	// ws hello visits: a line for a person, JSON or markdown when asked.
	p.Command("visits", "How often the page has been seen", func(c *plugin.Call) error {
		n, err := visits(c.Context())
		if err != nil {
			return err
		}
		return c.Show(struct {
			Visits int `json:"visits"`
		}{n})
	})
}
