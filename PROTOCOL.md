# The plugin protocol

How ws and a plugin talk. Version 1. The framework in this repo (`plugin`) speaks it for you; this is for ws's side, for a plugin in another language, and for settling an argument.

This is what's built: where ws and this differ, one of them has a bug.

It's the protocol of the plugin's process: the JSON-RPC between ws and the plugin, the plugin on disk and as it's published, and what it registers. A plugin's web UI is an app that ws shows in a page of its own, and how that app and ws talk is another protocol, the frame protocol, with versions of its own. It isn't here: it's [`ui-lib`'s `PROTOCOL.md`](https://github.com/withworkspace/ui-lib/blob/main/PROTOCOL.md). [The view](#the-view) says where the two meet.

## A plugin on disk

```
.ws/plugins/<name>/
  app/               the plugin's folder, or a link to one
    plugin.toml      name, version, command, build
    <the executable>
  registration.json  what it last registered (ws writes it)
  data/              the plugin's own files: plugin.db, plugin.log, view.sock
```

ws keeps its own files apart from the plugin's, so a folder linked in for development isn't written into.

```toml
name    = "jira"
version = "0.3.0"
command = ["./ws-jira"]
build   = ["go", "build", "-o", "ws-jira", "."]
```

`command` is run with the plugin's folder (`app/`) as its working directory.

`build` is optional: how the plugin's built from its source, run in its folder. ws runs it for a plugin added from a folder where what `command` starts isn't there, and when it's asked to (`ws plugin add --build`).

A plugin may exit on its own, with status 0, to be started afresh: one whose registration depends on config does when that config changes. ws starts it again at once.

## A plugin as it's published

A plugin is published as a repo on GitHub. `ws plugin add owner/repo` installs it.

A release of it holds, for each platform it's built for:

```
<name>_<tag>_<os>_<arch>.tar.gz     jira_v0.3.0_darwin_arm64.tar.gz
checksums.txt                       a line for each: "<sha256>  <file>", as sha256sum writes it
```

- `<os>` and `<arch>` are Go's: `darwin`, `linux`; `arm64`, `amd64`.
- The archive holds `plugin.toml` and what its `command` starts, at its top. It may hold other files and folders the plugin needs. It holds no links.
- ws installs the file whose name ends `_<os>_<arch>.tar.gz` for the machine it's on, from the latest release that isn't a prerelease, or the one tagged `owner/repo@<tag>`.
- ws checks the file against its line in `checksums.txt` before unpacking it. Read as anyone (with no `gh` logged in), a release's files are those `checksums.txt` lists, so a release needs one.
- The tag is `v` and `plugin.toml`'s `version`.

A repo with no release, or none for the machine, is installed from its source: ws fetches it and runs `plugin.toml`'s `build`.

The plugin-dev plugin gives a new plugin the workflow that publishes this on a tag.

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
- `view.listen` is a unix socket's path. A plugin that serves anything (its app's files, what its app asks it) listens there for HTTP before it answers. The file may be left from a run before: remove it first. It's usually `data/view.sock`, and somewhere shorter under the system's temporary folder when that path would be too long for a socket: listen wherever it says.
- `view.base` is the path the plugin's HTTP is served under ([The view](#the-view)).

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
  "ui": { "script": "/ui/app.js?v=3f9c2a", "style": "/ui/app.css?v=3f9c2a" },
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

- `views[].path` is where in the plugin's app the entry opens. `icon` is one of ws's icon names; one it doesn't have is drawn as a default.
- `ui` is the plugin's app: what ws's page for the plugin loads ([The view](#the-view)). A plugin has one app, whatever views it has: each view is a place in it.
  - `ui.script` is the app's entry, loaded as a module. A plugin with `views` has one. The plugin serves it as JavaScript (`text/javascript`).
  - `ui.style` is optional: a stylesheet, loaded before the app. The plugin serves it as `text/css`.
  - Both are paths in the plugin's own HTTP. Each starts with `/` (not `//`) and has no `..`. A query is kept as it's given, so a plugin can name what it built (`?v=<a hash of the file>`) and a browser asks again when it's built again.
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

What the app shows is stale. ws tells the open app (`ws:changed`, in the frame protocol), which asks the plugin again.

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

A plugin's web UI is one client-side app. ws shows it at `/x/<name>/…` in a page of its own, the shell, which loads the app the registration's `ui` names, from the plugin's HTTP. A plugin serves no HTML: no page, and nothing at a view's `path`, where a 404 is what's expected. The shell, what the app is given, and the messages between the app and ws are the frame protocol, which has its own versions: [`ui-lib`'s `PROTOCOL.md`](https://github.com/withworkspace/ui-lib/blob/main/PROTOCOL.md). [`@withworkspace/ui-lib`](https://www.npmjs.com/package/@withworkspace/ui-lib) speaks it for a page.

What's left here is the plugin's own HTTP: its app's files, and whatever its app asks it. ws proxies `GET|POST|… /plugins/<name>/<rest>` to the plugin's socket as `/<rest>`, with:

- `X-Forwarded-Prefix: /plugins/<name>`
- ws's own cookies removed

`ui.script` and `ui.style` are loaded through it: `/ui/app.js` is asked for as `/plugins/<name>/ui/app.js`.

Before the frame protocol, a plugin served its own pages here, with ws's `/plugin-kit.js` and `/plugin-kit.css` in them. Both files are gone, and so is the table of the frame's messages that was in this section. A plugin with `views` and no `ui.script` is one from then: ws says it has no app to show.
