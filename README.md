# workspace-kit

The framework for writing ws plugins in Go.

A plugin is one executable. ws's daemon starts it, and speaks to it over its stdin and stdout ([PROTOCOL.md](PROTOCOL.md)). This package speaks for it, so a plugin is what it declares:

```go
package main

import (
	"embed"

	"github.com/brudil/workspace-kit/plugin"
)

//go:embed agents.md migrations views
var files embed.FS

func main() {
	p := plugin.New(files)

	greeting := p.Config.String("greeting", "Greeting", plugin.Default("Hello"))

	p.View("Hello", plugin.Icon("sparkle"), func(v *plugin.View) {
		v.Page("/", "index.html", func(r *plugin.Request) (any, error) {
			return map[string]any{"Greeting": greeting.Get()}, nil
		})
	})

	p.Command("say", "Says the greeting", func(c *plugin.Call) error {
		return c.Say(greeting.Get() + ", workspace")
	})

	p.Run()
}
```

That's a plugin with a page in ws's web UI, a `ws hello say` command, a setting, and a guide that tells agents it's there. [examples/hello](examples/hello) is the whole of it, with a table of its own and a test.

## Starting one

The [plugin-dev](https://github.com/withworkspace/plugin-plugin-dev) plugin writes a new plugin that works as it's written, and builds and runs it in a workspace on every save. It's a plugin itself, written with this:

```bash
ws plugin-dev new notes
```

```bash
cd plugin-notes
```

```bash
ws plugin-dev watch
```

## What's built in

Declaring a thing and handling it are one call, and what ws is told the plugin adds is written from those calls.

| | |
|---|---|
| `p.Config.String`, `URL`, `Secret`, `List`, `Bool`, `Number`, `Enum` | A config key: in ws's Settings, asked for on install, read with `.Get()`. A change reaches `.Get()` at once. |
| `p.View`, `v.Page`, `v.Post`, `v.Handle` | Pages: a template in `views/` and a function returning its data, wrapped in ws's look. No JavaScript to build. |
| `p.Command` | `ws <plugin> <name>`. Return data and it's printed as a table for a person, JSON for `-f json`, markdown for `-f llm`. |
| `p.DB()` | A SQLite database in the plugin's data folder, with `migrations/*.sql` applied in order before anything runs. Pure Go: no C compiler. |
| `p.Sync` | The one place a plugin talks to its service: at start, on an interval, and on demand, one at a time, retried when it fails. Everything else reads the database. |
| `agents.md` | What agents are told of the plugin. |
| `p.Setup`, `p.OnConfig` | Declaring what depends on config, and hearing of a change. |
| `p.Log`, `p.Changed`, `p.Badge` | A log, telling open pages to reload, the count in the sidebar. |
| `p.WS`, `p.WSCommand` | Running one of ws's own commands in the workspace: `p.WS(ctx, "show", "m12", "-f", "llm")`. It's how a plugin reaches ws. |
| `.Timeout` on a command | How long it may run, where two minutes isn't it. `Timeout(0)` is until it's stopped. |
| `plugintest` | Runs the plugin inside a test, against a stand-in for ws. |

## A plugin's folder

```
plugin-hello/
  plugin.toml          name, version, command, and how it's built
  main.go
  agents.md            the guide for agents
  migrations/
    0001_visits.sql    the database, one file per change
  views/
    index.html         pages, as Go templates
  static/              served at static/, if it's there
```

## Trying the example

```bash
just build
```

```bash
cd examples/hello && ./ws-hello --registration
```

prints what the plugin would tell ws it adds. In a workspace:

```bash
ws plugin add --link /path/to/workspace-kit/examples/hello
```

## Not here yet

Things in ws's graph, palette actions, events, refs and an HTTP client. The design is `designs/24-plugins.md` in ws.
