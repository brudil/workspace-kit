# plugin

The framework for writing ws plugins in Go: `github.com/withworkspace/plugin`.

This repo is the framework, not a plugin. Plugins are the repos beside it named `plugin-<name>`.

A plugin is one executable. ws's daemon starts it, and speaks to it over its stdin and stdout ([PROTOCOL.md](PROTOCOL.md)). This package speaks for it, so a plugin is what it declares:

```go
package main

import (
	"embed"
	"encoding/json"
	"net/http"

	"github.com/withworkspace/plugin"
)

//go:embed agents.md migrations ui/dist
var files embed.FS

func main() {
	p := plugin.New(files)

	greeting := p.Config.String("greeting", "Greeting", plugin.Default("Hello"))

	p.UI("ui/dist")
	p.View("Hello", plugin.Icon("sparkle"))
	p.HandleFunc("GET /api/hello", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"greeting": greeting.Get()})
	})

	p.Command("say", "Says the greeting", func(c *plugin.Call) error {
		return c.Say(greeting.Get() + ", workspace")
	})

	p.Run()
}
```

That's a plugin with an app in ws's web UI, a `ws hello say` command, a setting, and a guide that tells agents it's there. [examples/hello](examples/hello) is the whole of it, with its app's source, a table of its own and a test.

## A plugin's web UI is an app

A plugin's web UI is one client-side app, and ws serves the page it runs in. The framework renders no pages, and a plugin has no HTML: no templates, no layout, nothing at a view's path.

- **The app** is a JS bundle, and a stylesheet if it wants one, built from the plugin's `ui/` into `ui/dist/` as `app.js` and `app.css`. `p.UI("ui/dist")` serves that folder and tells ws to load it.
- **The page** is ws's. It has ws's theme in it before the app loads, and an empty `<div id="root">` for the app to render into. ws serves it at every path under `/x/<name>/`, so a link to anywhere in the app loads the app there.
- **A view** is an entry in ws's sidebar that opens the app at a path: `p.View("Board", plugin.At("/board"))`. The app routes itself.
- **The app talks to ws** with [`@withworkspace/ui-lib`](https://www.npmjs.com/package/@withworkspace/ui-lib): where it is, its title, the theme, and when what it shows is stale. How they talk is the frame protocol, in [`ui-lib`'s `PROTOCOL.md`](https://github.com/withworkspace/ui-lib/blob/main/PROTOCOL.md).
- **The app talks to the plugin** over the plugin's own HTTP: `p.Handle("GET /api/issues", …)` here is `fetch(ws.api("/api/issues"))` there. `p.Changed()` tells the open app to ask again.

```ts
import { connect } from "@withworkspace/ui-lib";

const ws = await connect();
ws.setTitle("Hello");

async function show() {
  const { greeting } = await fetch(ws.api("/api/hello")).then((r) => r.json());
  ws.root.textContent = `${greeting}, workspace`;
}
ws.onChanged(show);
show();
```

The app's built files are committed. `go:embed` needs them when the plugin is built, so with them Go alone builds, tests and installs a plugin, and only changing its app needs Node.

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

Install it with `ws plugin add withworkspace/plugin-plugin-dev`. A new plugin comes with the workflow that publishes its releases: push a tag `v<version>`, and `ws plugin add <owner>/<repo>` installs it anywhere ([how it's published](PROTOCOL.md#a-plugin-as-its-published)).

## What's built in

Declaring a thing and handling it are one call, and what ws is told the plugin adds is written from those calls.

| | |
|---|---|
| `p.Config.String`, `URL`, `Secret`, `List`, `Bool`, `Number`, `Enum` | A config key: in ws's Settings, asked for on install, read with `.Get()`. A change reaches `.Get()` at once. |
| `p.UI` | The plugin's app: a folder of its built files, with `app.js` and, if it has one, `app.css`. It's served at `/ui/`, and registered with a hash of each file, so a browser asks again when it's built again. |
| `p.View`, with `plugin.Icon`, `Under`, `At` | An entry in ws's sidebar, which opens the app at a path. |
| `p.Handle`, `p.HandleFunc` | What the app asks the plugin, as `net/http` takes it: `"GET /api/issues/{key}"`. A path nothing handles is a plain 404. |
| `p.Command` | `ws <plugin> <name>`. Return data and it's printed as a table for a person, JSON for `-f json`, markdown for `-f llm`. |
| `p.DB()` | A SQLite database in the plugin's data folder, with `migrations/*.sql` applied in order before anything runs. Pure Go: no C compiler. |
| `p.Sync` | The one place a plugin talks to its service: at start, on an interval, and on demand, one at a time, retried when it fails. Everything else reads the database. |
| `agents.md` | What agents are told of the plugin. |
| `p.Setup`, `p.OnConfig` | Declaring what depends on config, and hearing of a change. |
| `p.Log`, `p.Changed`, `p.Badge` | A log, telling the open app that what it shows is stale, the count in the sidebar. |
| `p.WS`, `p.WSCommand` | Running one of ws's own commands in the workspace: `p.WS(ctx, "show", "m12", "-f", "llm")`. It's how a plugin reaches ws. |
| `.Timeout` on a command | How long it may run, where two minutes isn't it. `Timeout(0)` is until it's stopped. |
| `plugintest` | Runs the plugin inside a test, against a stand-in for ws: its commands, its config, and its HTTP as its app would ask it. |

## A plugin's folder

```
plugin-hello/
  plugin.toml          name, version, command, and how it's built
  main.go
  agents.md            the guide for agents
  migrations/
    0001_visits.sql    the database, one file per change
  ui/
    package.json       the app: @withworkspace/ui-lib, and what builds it
    src/main.ts
    src/app.css
    dist/              the app as built (app.js, app.css): committed, and served at /ui/
```

After a change to the app, build it and commit `ui/dist/` with the change:

```bash
cd ui && npm install && npm run build
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
ws plugin add --link /path/to/plugin/examples/hello
```

`just ui` builds the example's app again, and `just ui-check` fails if what's committed in `examples/hello/ui/dist/` isn't what its source builds.

## Moving a plugin from before v0.2.0

Before v0.2.0 the framework rendered a plugin's pages: Go templates in `views/`, wrapped in a layout with ws's `/plugin-kit.js` and `/plugin-kit.css`. All of that is gone, from here and from ws. A plugin built on it still starts, and ws says in its view that it has no app to show. `ws plugin check` says what's wrong with one.

| Gone | In its place |
|---|---|
| `v.Page`, the templates in `views/`, and the template functions `url`, `reltime` and `pill` | The app renders. What a page's function returned is a JSON endpoint: `p.Handle`. |
| `v.Post`, and the redirect back | A handler the app posts to with `fetch`. |
| `v.Handle` | `p.Handle`: the same, on the plugin. |
| `p.View(title, func(v *plugin.View) { … })` | `p.View(title, options…)`, and `plugin.At(path)` where the entry doesn't open at `/`. |
| `plugin.Request` | `*http.Request`. |
| `static/`, served at `static/` | The app's folder, served at `/ui/`. |
| The error pages (a page that failed, a plugin that couldn't start) | A handler answers in plain text, and the app shows it: 503 with why while the database couldn't be brought up, 500 for a panic. |
| The sync footer on every page, and its Refresh | `p.SyncStates()` and `p.SyncNow()`, for a plugin whose app shows them. |
| `p.PageTimeout` | Nothing: a handler is given no time limit. |
| `plugintest`'s `Page` | `plugintest.Response`: `Get` and `Post` are as they were. |

## Releasing

A version of the framework is a tag `v<version>` on `main`: Go's module proxy serves it from there, and nothing else has to be built or published. A plugin depends on a tagged version (`go get github.com/withworkspace/plugin@v0.2.0`), not on a commit of `main`.

The framework is before 1.0, so a change that breaks plugins is a minor version. v0.2.0 is the one where pages went.

## Not here yet

Things in ws's graph, palette actions, events, refs and an HTTP client.
