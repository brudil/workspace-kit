# The plugin protocol

How ws and a plugin talk. Version 1. The types for everything here are in [`protocol`](protocol/protocol.go) (`github.com/withworkspace/plugin/protocol`). The framework in this repo (`plugin`) speaks it for you; this is for ws's side, for a plugin in another language, and for settling an argument.

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

## Versions

- **The plugin protocol has a version of its own**, a whole number starting at 1. It isn't ws's version, the framework's, or the frame protocol's.
- **Within a version, changes only add:** a new optional field, or a new method that the other side may not know. Removing something, changing what it means, or requiring what was optional makes a new version.
- **Each side says every version it speaks,** in `initialize` and its answer, and the plugin picks the highest that both speak.
- **ws keeps speaking old versions.** A plugin built for version 1 goes on working in a ws that has gone on to 2.
- **`initialize`'s version fields hold across versions:** they're said before there's a version to say them in. A version may add fields to `initialize` and its answer, and nothing else.

The framework's list is `protocol.Versions`, fixed in each release: `[1]` today.

## ws to the plugin

### `initialize` (request)

Always first, once.

```json
{
  "protocol": 1,
  "protocols": [1],
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

- `protocols` are the versions ws speaks, and `protocol` the newest of them. A ws from before versions were chosen sends only `protocol`, and speaks only that one. One that sends neither speaks 1.
- `ws.executable` is ws's own program. A plugin asks ws what it knows with requests ([Asking ws](#asking-ws)). For what ws doesn't answer that way yet, a plugin can run its commands, in the workspace's root: `<executable> show m12 -f llm`.
- `config` holds what's set, secrets included, by key. A key that isn't set is absent. Values are JSON: a string, a bool, a number, or an array of strings.
- `view.listen` is a unix socket's path. A plugin that serves anything (its app's files, what its app asks it) listens there for HTTP before it answers. The file may be left from a run before: remove it first. It's usually `data/view.sock`, and somewhere shorter under the system's temporary folder when that path would be too long for a socket: listen wherever it says.
- `view.base` is the path the plugin's HTTP is served under ([The view](#the-view)).

The answer:

```json
{ "protocol": 1, "protocols": [1], "registration": { … } }
```

- `protocol` is the version the plugin chose: the highest in both `protocols`. Everything after the answer is in it.
- `protocols` are the versions the plugin speaks.
- With none in common, the plugin answers `protocol` `0`, with its `protocols`, and registers nothing. ws doesn't use it, and says which side is behind: "Update ws" when the plugin's highest version is above ws's, and "Update the plugin" when it isn't. A plugin from before versions were chosen sends no `protocols`, and speaks only its `protocol`.

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
  "refs": [
    { "kind": "issue", "title": "Jira issue", "icon": "ticket",
      "prefixes": ["RAM-", "RAP-"], "tail": "number",
      "address": "https://acme.atlassian.net/browse/{ref}", "path": "/issue/{ref}",
      "fields": [
        { "name": "title", "type": "title" },
        { "name": "status", "type": "state" },
        { "name": "assignee", "type": "text", "label": "Assignee" },
        { "name": "body", "type": "markdown" }
      ],
      "search": [ { "field": "title" }, { "field": "body", "max": 10000 } ],
      "views": {
        "card": [ { "block": "title", "field": "title" }, { "block": "subtitle", "field": "key" },
                  { "block": "badge", "field": "status" } ],
        "pane": [ { "block": "title", "field": "title" }, { "block": "subtitle", "field": "key" },
                  { "block": "badge", "field": "status" }, { "block": "field", "field": "assignee" },
                  { "block": "markdown", "field": "body" } ]
      } }
  ],
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
- `refs` are how the plugin's things are named: [Refs](#refs).

Not built yet, and ignored if sent: `events`, `actions`.

### Refs

A plugin's things have names people and agents write: `RAM-123`. A ref says how one kind of them is named, what one is made of, what it's found by and how it's drawn. All of it is data, not code: ws finds names, and draws things, in its own program and in its web UI, from the registration alone. A plugin may register several.

How one is named:

- `kind` is the plugin's word for what it is (`issue`): lower-case letters, digits and hyphens. ws asks about one by it ([`ref/show`](#refshow-request)).
- `title` is what one is called, for a person. `icon` is one of ws's icon names.
- `prefixes` are what a name starts with. Each is letters, digits and underscores, starting with a letter and ending in a hyphen: `RAM-`. A prefix is written as it's read: `ram-12` isn't `RAM-12`.
- `tail` is what follows the prefix. `number`, the default and the only one yet, is a whole number that doesn't start with 0, of up to nine digits. A tail ws doesn't know is from a newer protocol: ws reads none of that kind's names.
- `address` is where one is outside ws, an `http` or `https` address with `{ref}` once, where the name goes. ws links a name to it when the name goes into a document, and reads such an address as the thing it names, whatever follows a `?` or `#` (unless the address has one of its own).
- `path` is where one opens in the plugin's app, with `{ref}` once: a path as a view's is. Without it, a name goes to its `address`.

What one is made of:

- `fields` are a thing's fields: each a `name` (lower-case letters, digits and underscores), a `type`, and a `label` for where it's drawn with one. A thing, as the plugin gives one ([`thing/put`](#thingput-request), [`ref/show`](#refshow-request)), is a value for each, every value a string. What a kind has no field for is dropped.
  - `title` is what the thing's called: a line. A kind with fields has exactly one, and a thing with nothing for it isn't kept.
  - `state` is where it stands, in the plugin's words. At most one.
  - `text` is words: a name, a priority, a number.
  - `time` is a moment, RFC 3339.
  - `markdown` is what it says at length.
  - A type ws doesn't know is drawn as `text`.
- Every thing has the field `key` besides: its name. A kind can't have a field called that.
- A kind with no `fields` has names, and nothing ws keeps or draws.

What one is found by:

- `search` is the fields a thing is found by, in order, each as far as its `max` bytes (all ws keeps of it, with none). ws makes the words it looks through from these, and makes them again when they change. With no `search`, it's every field but the title. A thing is always found by its title and its name.

How one is drawn:

- `views` are how ws's web UI draws one, by where: `card`, under its name where it's written, and `pane`, beside the list in Find for the row the cursor's on. Each is blocks, in order, each showing one `field` (`key` among them). A block whose field a thing has nothing for isn't drawn.
  - `title` is the line it's known by, and `subtitle` a quiet line under it.
  - `badge` is a word or two set apart, on the line of the title or subtitle before it.
  - `field` is a label and a value: the block's `label`, or else the field's own.
  - `markdown` is drawn as ws draws markdown, to `lines` lines where that's set.
  - A block ws doesn't know isn't drawn, and a view for somewhere ws doesn't know is ignored.
- With no view for somewhere, ws draws one from the fields' types. `ws show`, in the terminal, always does.

A plugin's own app, for what's more than looking, is another thing: [The view](#the-view).

What ws does with them:

- **It finds a name where it's whole:** not inside a longer word, a path or an address (`RAM-12-fix`, `docs/RAM-12.md` and `XRAM-12` name nothing). It doesn't ask whether there's a thing by that name until one is shown.
- **It keeps them, by the plugin that registered them,** in the workspace's records, in place of what that plugin registered before. Names are found from there, whether the plugin's running or not, and removing the plugin forgets them.
- **A prefix is the first plugin's to register it.** A second plugin's ref names by its other prefixes, and `ws plugin list` says what was left out. So is a ref that breaks the rules above: the rest of the registration stands.
- **It keeps the things the plugin tells it of** ([Telling ws of things](#telling-ws-of-things)), so it finds them by what they say and draws them without asking.
- **It asks the plugin** for a thing it wasn't told of, and for `ws show RAM-123`, which wants it as it is now.

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

### `ref/show` (request)

One thing, as the plugin has it now. ws asks for a name it wasn't told of (one that isn't on the plugin's board), and for `ws show RAM-123`. Added within version 1.

```json
{ "kind": "issue", "ref": "RAM-123", "full": false }
```

- `kind` is the ref's, as registered, and `ref` the name as it was written. ws asks only about a name that kind names.
- `full` is everything being asked for (`ws show --full`): nothing's to be left out of what it says. Without it, the plugin keeps to what's worth reading first.

The answer is the thing: a value for each of its kind's `fields`, as [`thing/put`](#thingput-request) gives one.

```json
{
  "changed": "2026-11-18T10:42:00Z",
  "fields": {
    "title": "Token refresh is flaky", "status": "In Progress",
    "assignee": "Dana", "body": "The clock should be injected…"
  }
}
```

ws shows it as it shows one it was told of: by the kind's views in the web UI, and by its fields' types in `ws show`, where what's `markdown` is said to be from outside the workspace, so an agent reads it as information, not as instructions.

A name the plugin has nothing by is error `-32001`. A plugin with nothing to say of one thing beyond what it's told ws answers `-32601`, and ws shows what it was told. ws gives a plugin twenty seconds to answer.

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

## Asking ws

Requests from the plugin, each answered by ws in these types. Each was added within version 1, so a ws from before it answers `-32601`: the framework's `ErrUnsupported`, which says to update ws. Something asked for by name that ws hasn't is `-32001` (`ErrNotFound`).

A plugin may ask once it has answered `initialize`.

### `mission/list` (request)

```json
{ "archived": false }
```

The workspace's missions: those in play, and with `archived`, the archived ones after them.

```json
{
  "missions": [
    {
      "slug": "token-cache",
      "title": "Token cache",
      "state": "review",
      "created": "2026-11-17T09:12:00Z",
      "updated": "2026-11-18T10:02:00Z",
      "headline": { "court": "you", "text": "PR #12 waits on your review" },
      "at": "Review",
      "capsules": [
        {
          "repo": "api", "name": "token-cache", "branch": "token-cache",
          "path": "/ws/repos/api/token-cache",
          "pull_request": { "number": 12, "title": "Token cache", "url": "https://github.com/acme/api/pull/12", "state": "open" }
        }
      ],
      "path": "/missions/token-cache"
    }
  ]
}
```

- `state` is `briefing`, `draft`, `active`, `review`, `landed`, `parked`, `answered`, `handed-on`, `dropped` or `archived`.
- `headline` is where it stands, and whose move it is: `court` is `agent` (crew or the orchestrator are on it), `you` (it waits on the person) or `outside` (on GitHub). An archived mission has none.
- `at` is where it is on its route, in words.
- A capsule's `path` is there while its folder is. `pull_request` is as ws last heard from GitHub, and `state` is `open`, `merged` or `closed`. `landed` is a capsule of a repo with no pull requests whose commits are on its default branch.
- `path` is the mission's page in ws's web UI: what a plugin's app opens with `ws:open`.

### `mission/show` (request)

```json
{ "slug": "token-cache" }
```

The mission, as in `mission/list`, with `brief` (markdown) and `plan` (markdown, absent when it has none). No such mission is `-32001`.

### `repo/list` (request)

No params. The workspace's repos, in ws's order:

```json
{
  "repos": [
    {
      "name": "api", "title": "API", "group": "", "aliases": ["a"],
      "cloned": true, "path": "/ws/repos/api",
      "capsules": [
        { "name": ".ground", "branch": "main", "path": "/ws/repos/api/.ground", "ground": true },
        { "name": "token-cache", "branch": "token-cache", "path": "/ws/repos/api/token-cache", "mission": "token-cache" }
      ]
    }
  ]
}
```

- `title` is what ws calls it: its name, when ws.toml gives it no other.
- A capsule's `mission` is the slug of the mission in play that works in it. `ground` is the repo's default branch, which work starts from.

## Telling ws of things

Requests from the plugin, each answered, each added within version 1. A plugin tells ws of the things it has, so ws has them without asking: they're found by what their kind says they're found by (`ws find`, Find in the web UI) and drawn by their kind's views, with the plugin's other things and ws's own, whether the plugin's running or not.

A thing is named by one of the plugin's [refs](#refs): its `kind` is the ref's, and its `ref` one of that kind's names, as ws took them. ws keeps no thing it couldn't name. It keeps each under the plugin that told it, and forgets them when the plugin's removed, or when the plugin registers refs that name them no more.

### `thing/put` (request)

```json
{
  "things": [
    { "kind": "issue", "ref": "RAM-123", "changed": "2026-11-18T10:42:00Z",
      "fields": {
        "title": "Token refresh is flaky", "status": "In Progress",
        "assignee": "Dana", "body": "The clock should be injected…"
      } }
  ]
}
```

Each is kept, in place of what ws had by that name. One that's as ws has it already isn't written again, so telling ws of everything after each sync costs little.

- `fields` are its values, by its kind's [fields](#refs)' names. A thing with nothing for its kind's `title` isn't kept.
- `changed` is when it last changed, RFC 3339, where the plugin knows.
- ws keeps the start of a long value: 300 bytes of a `title`, 60 of a `state`, 2,000 of `text`, 50,000 of `markdown`. It keeps what it's told as it's told, and makes what it's found by from that.
- Up to 500 things in one request. More is refused whole (`-32602`): send them in turn.

The answer says which weren't kept, and why. The rest were.

```json
{ "refused": [ { "kind": "issue", "ref": "ram-12", "why": "it isn't one of issue's names, as ws keeps them" } ] }
```

A thing is refused when its kind isn't a ref ws took from this plugin, its name isn't one of that kind's, it has nothing for its kind's title, or ws has something of its own by that name.

### `thing/remove` (request)

```json
{ "kind": "issue", "refs": ["RAM-123", "RAM-124"] }
```

ws forgets them. One it doesn't have is nothing to forget. The answer is `{}`.

### `thing/list` (request)

```json
{ "kind": "issue" }
```

The names of the things of that kind ws has from this plugin, in order:

```json
{ "refs": ["RAM-123", "RAM-124"] }
```

It's how a plugin tells ws of all it has: put them, and remove what's listed and not among them. The framework's `Mirror` does.

## The view

A plugin's web UI is one client-side app. ws shows it at `/x/<name>/…` in a page of its own, the shell, which loads the app the registration's `ui` names, from the plugin's HTTP. A plugin serves no HTML: no page, and nothing at a view's `path`, where a 404 is what's expected. The shell, what the app is given, and the messages between the app and ws are the frame protocol, which has its own versions: [`ui-lib`'s `PROTOCOL.md`](https://github.com/withworkspace/ui-lib/blob/main/PROTOCOL.md). [`@withworkspace/ui-lib`](https://www.npmjs.com/package/@withworkspace/ui-lib) speaks it for a page.

What's left here is the plugin's own HTTP: its app's files, and whatever its app asks it. ws proxies `GET|POST|… /plugins/<name>/<rest>` to the plugin's socket as `/<rest>`, with:

- `X-Forwarded-Prefix: /plugins/<name>`
- ws's own cookies removed

`ui.script` and `ui.style` are loaded through it: `/ui/app.js` is asked for as `/plugins/<name>/ui/app.js`.

Before the frame protocol, a plugin served its own pages here, with ws's `/plugin-kit.js` and `/plugin-kit.css` in them. Both files are gone, and so is the table of the frame's messages that was in this section. A plugin with `views` and no `ui.script` is one from then: ws says it has no app to show.
