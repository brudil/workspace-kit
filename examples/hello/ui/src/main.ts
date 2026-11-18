// The hello plugin's app. ws serves the page this runs in, with its theme
// already there and an empty #root. This connects to ws, asks the plugin
// for its greeting, and renders into #root.

import { type Client, connect } from "@withworkspace/ui-lib";

// What the plugin answers, at /api/hello and /api/visits (main.go).
interface Hello {
  greeting: string;
  visits: number;
}

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

  // The plugin's own HTTP is at ws.api(path).
  async function ask(path: string, method = "GET"): Promise<Hello> {
    const res = await fetch(ws.api(path), { method });
    if (!res.ok) throw new Error((await res.text()) || `${path} answered ${res.status}`);
    return (await res.json()) as Hello;
  }

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
  ask("/api/visits", "POST").then(show, failed);

  // The plugin said what this shows is stale (the greeting was changed in
  // Settings, say): ask again.
  ws.onChanged(() => ask("/api/hello").then(show, failed));
}

void main();
