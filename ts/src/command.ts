// A plugin's commands: ws <plugin> <command> [args] [flags], run in the
// plugin and answered with an exit code, its output streamed to ws.

import type { CommandRunParams, Terminal } from "./protocol.gen.ts";
import { InputPiped, MethodCommandOutput, MethodCommandRead, ReadAtOnce, Stderr, Stdout } from "./protocol.gen.ts";
import { FormatJSON, FormatLLM, FormatTable, markdownTable, tabulate, textTable, type Format } from "./output.ts";
import type { Host } from "./host.ts";

interface Flag {
  name: string;
  usage: string;
  takesValue: boolean;
}

/** A command, as declared. Its methods say more of it, and return it. */
export class Command {
  /** @internal */ readonly flags: Flag[] = [];
  /** @internal */ usage = "";
  /** @internal */ conversation = false;
  /** @internal */ label_ = "";
  /** @internal */ timeoutMs?: number;
  /** @internal */ readonly name: string;
  /** @internal */ readonly short: string;
  /** @internal */ readonly fn: (c: Call) => unknown;

  constructor(name: string, short: string, fn: (c: Call) => unknown) {
    this.name = name;
    this.short = short;
    this.fn = fn;
  }

  /** A flag it takes with no value: --all. */
  flag(name: string, usage: string): this {
    this.flags.push({ name, usage, takesValue: false });
    return this;
  }

  /** A flag it takes with a value: --status done. */
  option(name: string, usage: string): this {
    this.flags.push({ name, usage, takesValue: true });
    return this;
  }

  /** What follows it, for its help: [NAME]. */
  args(usage: string): this {
    this.usage = usage;
    return this;
  }

  /** A conversation's agent may run it itself, not only propose it. */
  conversations(): this {
    this.conversation = true;
    return this;
  }

  /** What it's doing, as a conversation shows an agent's step that ran it: "Pulling up {1}". */
  label(label: string): this {
    this.label_ = label;
    return this;
  }

  /** How long it may run, in milliseconds: the plugin's commandTimeout otherwise. */
  timeout(ms: number): this {
    this.timeoutMs = ms;
    return this;
  }
}

/** An error that ends a command with its own exit code. */
export class ExitError extends Error {
  readonly code: number;
  constructor(code: number, message: string) {
    super(message);
    this.name = "ExitError";
    this.code = code;
  }
}

/** One run of a command: what it was run with, and where its output goes. */
export class Call {
  /** The arguments after the command, flags taken out. */
  readonly args: string[] = [];
  /** How what it prints is to be printed: -f table, json or llm. */
  format: Format = FormatTable;
  /** Aborts when the command's run out of time, or whoever ran it gave up (Ctrl-C). */
  signal!: AbortSignal;
  private flags = new Set<string>();
  private options = new Map<string, string>();
  private left: Buffer = Buffer.alloc(0);
  private eof = false;
  private host: Host;
  private cmd: Command;
  private run: CommandRunParams;

  constructor(host: Host, cmd: Command, run: CommandRunParams) {
    this.host = host;
    this.cmd = cmd;
    this.run = run;
  }

  /** The argument at i, or "". */
  arg(i: number): string {
    return this.args[i] ?? "";
  }

  /** Whether a flag was given. */
  flag(name: string): boolean {
    return this.flags.has(name);
  }

  /** An option's value, or "". */
  option(name: string): string {
    return this.options.get(name) ?? "";
  }

  /** The folder it was run from. */
  get dir(): string {
    return this.run.dir;
  }

  /** The terminal's width, or 0 when the output isn't going to one. */
  get width(): number {
    return this.run.terminal.tty ? this.run.terminal.width : 0;
  }

  /** Whether its standard input is a pipe or a file, not a person typing. */
  get piped(): boolean {
    return this.run.input === InputPiped;
  }

  /** Writes text to its standard output, as it is. */
  out(text: string) {
    if (text) this.host.notify(MethodCommandOutput, { id: this.run.id, stream: Stdout, data: text });
  }

  /** Writes text to its standard error, as it is. */
  err(text: string) {
    if (text) this.host.notify(MethodCommandOutput, { id: this.run.id, stream: Stderr, data: text });
  }

  /** Prints a line: for -f json, {"message": text}. */
  say(text: string) {
    if (this.format === FormatJSON) return this.json({ message: text });
    this.out(text + "\n");
  }

  /** Prints one thing, a field to a line: for -f json the value as JSON, for -f llm a markdown list. */
  show(one: object) {
    if (this.format === FormatJSON) return this.json(one);
    const { cols, cells } = tabulate([one]);
    const row = cells[0];
    if (!row) return;
    const width = Math.max(...cols.map((c) => [...c].length));
    this.out(
      cols
        .map((c, i) => (this.format === FormatLLM ? `- **${c}**: ${row[i]}\n` : `${(c + ":").padEnd(width + 1)}  ${row[i]}\n`))
        .join(""),
    );
  }

  /** Prints rows: a table for a person, JSON for -f json, a markdown table for -f llm. */
  list(rows: readonly object[]) {
    if (this.format === FormatJSON) return this.json(rows);
    const { cols, cells } = tabulate(rows);
    if (cells.length === 0) return this.out("Nothing to show.\n");
    if (this.format === FormatLLM) return this.out(markdownTable(cols, cells));
    this.out(textTable(cols, cells, this.width, this.run.terminal.color));
  }

  private json(v: unknown) {
    this.out(JSON.stringify(v, null, 2) + "\n");
  }

  /**
   * Reads what's on its standard input, all of it, as text. A command run
   * with nothing to read reads "".
   */
  async stdin(): Promise<string> {
    const parts: Buffer[] = [];
    for (;;) {
      const got = await this.read(ReadAtOnce);
      if (got === null) break;
      parts.push(got);
    }
    return Buffer.concat(parts).toString("utf8");
  }

  /** Reads what's next on its standard input, up to max bytes: null at its end. */
  async read(max: number): Promise<Buffer | null> {
    if (!this.run.input) return null;
    while (this.left.length === 0) {
      if (this.eof) return null;
      const got = await this.host.ask(MethodCommandRead, { id: this.run.id, max: Math.min(max, ReadAtOnce) }, this.signal);
      this.left = got.data ? Buffer.from(got.data, "base64") : Buffer.alloc(0);
      this.eof = !!got.eof;
      if (this.left.length === 0 && !this.eof) throw new Error("reading the command's input: ws gave nothing, and didn't say it was the end");
    }
    const n = Math.min(max, this.left.length);
    const out = this.left.subarray(0, n);
    this.left = this.left.subarray(n);
    return out;
  }

  /** @internal Takes the flags out of what it was run with. Whether help was asked for. */
  parse(args: string[]): boolean {
    let help = false;
    const declared = new Map(this.cmd.flags.map((f) => [f.name, f]));
    for (let i = 0; i < args.length; i++) {
      const a = args[i]!;
      if (a === "--") {
        this.args.push(...args.slice(i + 1));
        return help;
      }
      if (a === "-h" || a === "--help") {
        help = true;
        continue;
      }
      if (a === "-f" || a === "--format" || a.startsWith("--format=") || a.startsWith("-f=")) {
        let v = a.includes("=") ? a.slice(a.indexOf("=") + 1) : undefined;
        if (v === undefined) {
          if (i + 1 >= args.length) throw new UsageError(`${a} needs a format: table, json or llm`);
          v = args[++i]!;
        }
        if (v !== FormatTable && v !== FormatJSON && v !== FormatLLM) throw new UsageError(`unknown format "${v}": it's table, json or llm`);
        this.format = v;
        continue;
      }
      if (a.startsWith("--")) {
        const eq = a.indexOf("=");
        const name = eq < 0 ? a.slice(2) : a.slice(2, eq);
        const f = declared.get(name);
        if (!f) throw new UsageError(`unknown flag --${name}`);
        if (!f.takesValue) {
          if (eq >= 0) throw new UsageError(`--${name} takes no value`);
          this.flags.add(name);
          continue;
        }
        let value = eq < 0 ? undefined : a.slice(eq + 1);
        if (value === undefined) {
          if (i + 1 >= args.length) throw new UsageError(`--${name} needs a value`);
          value = args[++i]!;
        }
        this.options.set(name, value);
        continue;
      }
      this.args.push(a);
    }
    return help;
  }
}

/** @internal A command run wrongly: it's said, with the command's help. */
export class UsageError extends Error {}

export const isHelp = (arg: string | undefined) => arg === "help" || arg === "--help" || arg === "-h";

/** @internal The plugin's help: its commands, and the flags every command takes. */
export function help(plugin: string, description: string, commands: readonly Command[]): string {
  let out = description ? `${description}\n\n` : "";
  out += `Usage:\n  ws ${plugin || "<plugin>"} <command> [flags]\n\nCommands:\n`;
  const sorted = [...commands].sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
  const width = Math.max(0, ...sorted.map((c) => c.name.length));
  for (const c of sorted) out += `  ${c.name.padEnd(width)}  ${c.short}\n`;
  return out + "\nFlags:\n  -f, --format  table, json or llm (default table)\n  -h, --help    this help\n";
}

/** @internal One command's help. */
export function helpFor(plugin: string, c: Command): string {
  let out = c.short ? `${c.short}\n\n` : "";
  let usage = `ws ${plugin || "<plugin>"} ${c.name}`;
  if (c.usage) usage += ` ${c.usage}`;
  out += `Usage:\n  ${usage} [flags]\n\nFlags:\n`;
  const lines = c.flags.map((f) => ({ flag: `    --${f.name}${f.takesValue ? " <value>" : ""}`, usage: f.usage }));
  lines.push({ flag: "-f, --format <format>", usage: "table, json or llm (default table)" }, { flag: "-h, --help", usage: "this help" });
  const width = Math.max(...lines.map((l) => l.flag.length));
  for (const l of lines) out += `  ${l.flag.padEnd(width)}  ${l.usage}\n`;
  return out;
}

export type { Terminal };
