// The hello plugin's app. ws serves the page this runs in, with its theme
// already there and an empty #root. This connects to ws, asks the plugin
// for its greeting, and renders into #root.

import { type Client, connect, endpoints } from "@withworkspace/ui-lib";
// What the plugin's endpoints take and answer, written from its Go
// (main.go) by `go tool apigen`.
import type { Api, Hello } from "./api.gen";

async function main() {
  let ws: Client;
  try {
    ws = await connect();
  } catch (err) {
    // No ws to talk to, or one that speaks another frame protocol: the
    // error says which, and what to update.
    (document.getElementById("root") ?? document.body).textContent = err instanceof Error ? err.message : String(err);
    return;
  }
  ws.setTitle("Hello");

  // The plugin's endpoints, by what each takes and answers: a path that
  // isn't one of them doesn't compile, and what comes back is a Hello
  // because the Go says so.
  const plugin = endpoints<Api>(ws);

  function show(hello: Hello) {
    const h1 = document.createElement("h1");
    h1.textContent = `${hello.greeting}, workspace`;
    const seen = document.createElement("p");
    seen.className = "dim";
    seen.textContent = `Seen ${hello.visits} ${hello.visits === 1 ? "time" : "times"}.`;
    const main = document.createElement("main");
    main.append(h1, seen);
    ws.root.replaceChildren(main);
  }

  function failed(err: unknown) {
    const p = document.createElement("p");
    p.className = "error";
    p.textContent = err instanceof Error ? err.message : String(err);
    const main = document.createElement("main");
    main.append(p);
    ws.root.replaceChildren(main);
  }

  // Each look at the app is counted, as it loads.
  plugin.post("/api/visits").then(show, failed);

  // The plugin said what this shows is stale (the greeting was changed in
  // Settings, say): ask again.
  ws.onChanged(() => plugin.get("/api/hello").then(show, failed));
}

void main();
