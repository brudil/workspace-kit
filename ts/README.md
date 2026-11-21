# @withworkspace/plugin

The framework for writing a ws plugin in TypeScript, on Node (22.18 or later) or Bun. It's the Go kit's way (`github.com/withworkspace/plugin`, at this repo's root) and speaks the same protocol: both kits' types are generated from one spec, in [withworkspace/protocol](https://github.com/withworkspace/protocol), and both are released together, at the same version.

A plugin is one process. ws's daemon starts it and speaks to it over its stdin and stdout ([PROTOCOL.md](../PROTOCOL.md)); this speaks for it, so a plugin is what it declares and a call to `run`:

```ts
import { Plugin, blocks, fields } from "@withworkspace/plugin";

const p = new Plugin({ root: import.meta.dirname });
p.describe("Says hello");

const greeting = p.config.string("greeting", "Greeting", { default: "Hello" });

// The app, and its entry in ws's sidebar: ws loads it in a page of its own.
p.ui("ui/dist");
p.view("Hello", { icon: "sparkle" });
p.get("/api/hello", () => ({ greeting: greeting.get() }));

// ws hello say [NAME]
p.command("say", "Says the greeting", (c) => c.say(`${greeting.get()}, ${c.arg(0) || "workspace"}`)).args("[NAME]");

// VISIT-3, wherever it's written, is one of the plugin's things.
p.ref("visit", "Visit")
  .prefix("VISIT-")
  .fields(fields.title("title"), fields.time("at", "When"))
  .card(blocks.title("title"), blocks.field("at"))
  .show(async ({ name }) => ({ name, fields: { title: name } }));

p.run();
```

```toml
# plugin.toml
name    = "hello"
version = "0.1.0"
command = ["node", "main.ts"]
```

Node runs a `.ts` file as it is, and so does Bun, so a plugin needs no build. `node main.ts --registration` prints what it would register.

## What's here

- **Declaring:** `describe`, `config` (string, url, secret, enum, list, bool, number, map), `setup` for what depends on config (a key read there that changes restarts the plugin), `onConfig`, `onLooking`, `guide` (or `agents.md`).
- **The app:** `ui`, `view`, and endpoints (`get`, `post`, `put`, `patch`, `delete`, `handle`) on the unix socket ws gives, JSON in and out; `NotFoundError` is a 404 and `StatusError` any other. `changed` and `badge` tell ws's page.
- **Commands:** `command` with `args`, `flag`, `option`, `label`, `conversations`, `timeout`; a `Call` reads its args and stdin, and prints with `say`, `show` and `list` in table, JSON or markdown (`-f`).
- **Actions:** `action`, which ask (`ask`, `pick`) before they do anything, and `say` or `open`.
- **Refs and things:** `ref` with prefixes, fields, search, card and pane views, actions and `show`; `put`, `remove` and `mirror` tell ws of things.
- **Asking ws:** `missions`, `mission`, `show`, `showIn`, `repos`, `startConversation`.

Not yet, where the Go kit has them: syncs, proposals, remotes (things in a repo), config tables, a database of the plugin's own, running ws's commands. The protocol's types for all of them are in `@withworkspace/plugin/protocol` already.

## Developing it

```
npm ci
npm run typecheck
npm test        # the kit against ws's side of the protocol, played over streams
npm run build   # dist/, what's published
```

`examples/hello` is the Go kit's hello in TypeScript: it serves the Go hello's app as built, since it answers what that app asks just as the Go one does. `ws plugin add --link ts/examples/hello` runs it in a workspace.

`src/protocol.gen.ts` is generated: change the protocol in its spec, and bring it here with `just protocol` at the repo's root.
