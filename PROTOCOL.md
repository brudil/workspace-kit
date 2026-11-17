# The plugin protocol

How ws and a plugin talk. Version 1. The framework in this repo (`plugin`) speaks it for you; this is for ws's side, for a plugin in another language, and for settling an argument.

The design it comes from is `designs/24-plugins.md` in ws. Where they differ, this is what's built.

## A plugin on disk

```
.ws/plugins/<name>/
  app/               the plugin's folder, or a link to one
    plugin.toml      name, version, command
    <the executable>
  registration.json  what it last registered (ws writes it)
  data/              the plugin's own files: plugin.db, plugin.log, view.sock
```

ws keeps its own files apart from the plugin's, so a folder linked in for development isn't written into.

```toml
name    = "jira"
version = "0.3.0"
command = ["./ws-jira"]
```

`command` is run with the plugin's folder (`app/`) as its working directory.

A plugin may exit on its own, with status 0, to be started afresh: one whose registration depends on config does when that config changes. ws starts it again when it's next needed.

## The connection

- JSON-RPC 2.0, one message per line, over the plugin's stdin (ws to the plugin) and stdout (the plugin to ws).
- The plugin's stderr is its log. ws appends it to `data/plugin.log`.
- Either side may send requests and notifications.
- A line that isn't JSON is skipped. A plugin must write nothing but messages to stdout.
- Giving up on a request is the notification `$/cancel_request` with `{"requestId": <id>}`. The side answering stops and answers with error `-32800`.
- A method either side doesn't know is answered with `-32601` (requests) or dropped (notifications).
- ws closing the plugin's stdin means: exit.

## ws to the plugin

### `initialize` (request)

Always first, once.

```json
{
  "protocol": 1,
  "ws": { "version": "0.42.0", "executable": "/usr/local/bin/workspace" },
  "plugin": {
    "name": "jira",
    "version": "0.3.0",
    "dir": "/ws/.ws/plugins/jira/app",
    "data": "/ws/.ws/plugins/jira/data"
  },
  "workspace": { "root": "/ws" },
  "config": { "site": "https://acme.atlassian.net", "projects": ["PROJ"] },
  "view": { "listen": "/ws/.ws/plugins/jira/data/view.sock", "base": "/plugins/jira" }
}
```

- `ws.executable` is ws's own program. A plugin reaches ws by running its commands, in the workspace's root: `<executable> show m12 -f llm`. ws's commands are its interface to a plugin as they are to anything else; there are no methods a plugin calls ws by yet.
- `config` holds what's set, secrets included, by key. A key that isn't set is absent. Values are JSON: a string, a bool, a number, or an array of strings.
- `view.listen` is a unix socket's path. A plugin with a view listens there for HTTP before it answers. The file may be left from a run before: remove it first. It's usually `data/view.sock`, and somewhere shorter under the system's temporary folder when that path would be too long for a socket: listen wherever it says.
- `view.base` is the path the view is served under.

The answer:

```json
{ "protocol": 1, "registration": { … } }
```

A plugin must answer even when its config is incomplete. ws asks nothing more of it until what's required is set.

### The registration

Every field is optional.

```json
{
  "description": "Jira issues beside your missions",
  "views": [
    { "title": "Jira", "icon": "ticket", "under": "tasks", "path": "/" }
  ],
  "commands": {
    "name": "jira",
    "short": "Jira issues",
    "subcommands": [
      { "name": "mine", "short": "Issues assigned to you", "args": "" }
    ]
  },
  "agent": {
    "roles": ["crew", "orchestrator", "conversation"],
    "guide": "markdown"
  },
  "syncs": [ { "name": "issues", "every": 300 } ],
  "config_version": 1,
  "config": [
    { "key": "site", "title": "Jira site", "type": "url", "required": true,
      "help": "", "example": "https://acme.atlassian.net", "scope": "shared" },
    { "key": "token", "title": "API token", "type": "secret", "required": true },
    { "key": "projects", "title": "Projects to show", "type": "list", "default": [] },
    { "key": "kind", "title": "Kind", "type": "enum", "options": ["a", "b"], "default": "a" }
  ]
}
```

- `views[].path` is where in the plugin's view the entry opens. `icon` is one of ws's icon names; one it doesn't have is drawn as a default.
- `commands.name` is the word after `ws`. It defaults to the plugin's name.
- `agent.roles` empty means every role.
- `config[].type` is one of `string`, `url`, `bool`, `number`, `enum`, `list`, `secret`. `scope` is `shared` (the default) or `local`; a secret is always local.
- `syncs` is for show (`ws plugin list`); ws doesn't run them.

Not built yet, and ignored if sent: `events`, `actions`, `refs`, `things`.

### `config/changed` (notification)

```json
{ "config": { … } }
```

The whole config, as in `initialize`.

### `command/run` (request)

Someone ran `ws jira mine --all -f llm`.

```json
{
  "id": "c7",
  "args": ["mine", "--all", "-f", "llm"],
  "dir": "/ws/repos/api/token-cache",
  "terminal": { "tty": true, "width": 120, "color": true }
}
```

- `id` is ws's name for this run. Its output carries it.
- `args` is everything after the command's name, untouched.
- `dir` is where it was run from.

While it runs, the plugin sends `command/output`. Its answer ends the command:

```json
{ "exit": 0 }
```

Ctrl-C is `$/cancel_request` for this request.

### `shutdown` (request)

Finish and exit. The answer is `null`. ws closes stdin after it, and kills a plugin still running five seconds later.

## The plugin to ws

### `command/output` (notification)

```json
{ "id": "c7", "stream": "stdout", "data": "PROJ-1  Token cache\n" }
```

`id` is the `command/run`'s. `stream` is `stdout` or `stderr`.

Sent before the command's answer, it's delivered before the command ends.

### `log` (notification)

```json
{ "level": "error", "message": "sync issues: 401 from Jira" }
```

`level` is `debug`, `info`, `warn` or `error`. The last error shows in `ws plugin list`.

### `view/changed` (notification)

```json
{}
```

What the view shows is stale. ws tells open pages of it, and the page's script reloads them.

### `view/badge` (notification)

```json
{ "path": "/", "count": 3 }
```

The count beside the view's sidebar entry (the view registered with that `path`). `0` removes it.

### `sync/state` (notification)

```json
{ "name": "issues", "at": "2026-11-17T10:42:00Z", "took_ms": 840, "error": "" }
```

A sync finished. For `ws plugin list`.

## The view

ws proxies `GET|POST|… /plugins/<name>/<rest>` to the plugin's socket as `/<rest>`, with:

- `X-Forwarded-Prefix: /plugins/<name>`
- ws's own cookies removed

The page is shown in an iframe at `/x/<name>/<rest>`, same-origin with ws. Everything after the plugin's name is the plugin's: it routes it, and says when there's nothing at a path. When ws's address moves within the plugin and the page didn't cause it, ws loads the page at the new path, unless the page routes itself (`ws:routing`, below). ws serves two files for it:

- `/plugin-kit.js`: the page's side of the frame's messages, below
- `/plugin-kit.css`: ws's look for a plugin's page

### The frame's messages

`window.postMessage`, each `{ "type": "ws:…", … }`.

| Message | From | Fields | What it's for |
|---|---|---|---|
| `ws:ready` | page | `path`, `title` | The page loaded. `path` is its path without the base (`/browse/PROJ-1`), with its query. |
| `ws:hello` | ws | `scheme` (`light` or `dark`), `vars` (CSS custom properties by name) | The answer to `ws:ready`: the theme |
| `ws:theme` | ws | as `ws:hello` | The theme changed |
| `ws:path` | page | `path` | The page moved without loading (its own routing) |
| `ws:routing` | page | | The page routes itself: when ws's address moves within the plugin, tell it (`ws:navigate`), don't load it again. Said again each time the page loads. |
| `ws:navigate` | ws | `path` | ws's address moved to this path in the plugin, without the page having said so (the sidebar, back and forward, a link in ws). Sent only to a page that said `ws:routing`; any other is loaded at the path. |
| `ws:title` | page | `title` | The page's title |
| `ws:open` | page | `path` | Go to a page of ws's |
| `ws:key` | page | `key`, `meta`, `ctrl`, `shift`, `alt` | A key ws owns was pressed in the frame |
| `ws:changed` | ws | | The plugin said `view/changed` |
