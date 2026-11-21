// hello-ts is the Go kit's hello (examples/hello), in TypeScript: an app in
// ws's web UI, two commands, a config key, an action, and names for what's
// in it. It serves the Go hello's app as built (examples/hello/ui/dist),
// since it answers what that app asks just as the Go one does; it keeps its
// visits in a file of its own rather than a database.
//
// Node 22.18 or later runs it as it is (command = ["node", "main.ts"]), and
// so does Bun. A plugin of its own imports "@withworkspace/plugin" from npm;
// this one imports the kit from beside it.

import { readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { NotFoundError, Plugin, UnsupportedError, blocks, fields, stamp, type Thing } from "../../src/index.ts";

/** Hello is what the app shows: the greeting, and how often the app has been seen. */
interface Hello {
  greeting: string;
  visits: number;
}

const p = new Plugin({ root: import.meta.dirname });
p.describe("Says hello, in TypeScript");

// Config: declared and read in one. Settings draws the field.
const greeting = p.config.string("greeting", "Greeting", { default: "Hello" });

// Each visit's moment, in the plugin's data folder.
const file = () => join(p.data, "visits.json");
const visits = async (): Promise<number[]> => JSON.parse(await readFile(file(), "utf8").catch(() => "[]"));

// The app, and its entry in ws's sidebar. ws serves the page and loads the
// app in it: the plugin has no HTML.
p.ui("../../../examples/hello/ui/dist");
p.view("Hello (TS)", { icon: "sparkle" });

const hello = async (): Promise<Hello> => ({ greeting: greeting.get(), visits: (await visits()).length });
p.get("/api/hello", hello);

// VISIT-3 is the third look at the app. ws finds a name like it wherever
// it's written; what one is made of, and how it's drawn, is said here, as
// data: ws draws it.
const visited = (n: number, at: Date): Thing => ({
  name: `VISIT-${n}`,
  changed: at,
  fields: {
    title: `Visit ${n}`,
    at: stamp(at),
    said: `The app was looked at on ${at.toLocaleDateString("en-GB", { day: "numeric", month: "long", year: "numeric", timeZone: "UTC" })}, and said: **${greeting.get()}**.`,
  },
});
const visit = p
  .ref("visit", "Visit")
  .prefix("VISIT-")
  .fields(fields.title("title"), fields.time("at", "When"), fields.markdown("said"))
  .card(blocks.title("title"), blocks.subtitle(blocks.Key), blocks.field("at"))
  .pane(blocks.title("title"), blocks.subtitle(blocks.Key), blocks.field("at"), blocks.markdown("said"))
  // One visit, as it is now: for one ws wasn't told of, and for ws show VISIT-3.
  .show(async ({ name }) => {
    const n = Number(name.slice("VISIT-".length));
    const at = (await visits())[n - 1];
    if (at === undefined) throw new NotFoundError();
    return visited(n, new Date(at * 1000));
  });

// Each look at the app is counted: it posts here as it loads. ws is told of
// the visit, so it's found there by what it says and drawn without asking.
p.post(
  "/api/visits",
  async () => {
    const all = await visits();
    all.push(Math.floor(Date.now() / 1000));
    await writeFile(file(), JSON.stringify(all));
    await visit.put([visited(all.length, new Date())]).catch((err) => {
      if (!(err instanceof UnsupportedError)) p.log.warn(`telling ws of a visit: ${err.message}`); // an older ws finds none of them
    });
    return hello();
  },
  { body: false },
);

// Something to do, from Find: it asks who to greet, and says it.
p.action("greet", "Say hello to…", async (a) => {
  const name = await a.ask("Who to?", "A name");
  a.say(`${greeting.get()}, ${name}`);
}).icon("sparkle");

// ws hello-ts say
p.command("say", "Says the greeting", (c) => c.say(`${greeting.get()}, ${c.arg(0) || "workspace"}`)).args("[NAME]");

// ws hello-ts visits: a line for a person, JSON or markdown when asked.
p.command("visits", "How often the app has been seen", async (c) => c.show({ visits: (await visits()).length }));

p.run();
