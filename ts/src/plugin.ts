// The plugin: what it declares before run, and what it's running with
// after. ws's daemon starts it and speaks to it over its stdin and stdout
// (the plugin protocol); this speaks for it, so a plugin is what it
// declares and a call to run.

import { readFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { Writable, type Readable } from "node:stream";
import type {
  ActionRunParams,
  CommandRunParams,
  ConversationStarted,
  InitializeParams,
  InitializeResult,
  Mission,
  MissionDetail,
  NotificationsToWS,
  Registration,
  RefShowParams,
  RefShowResult,
  Repo,
  RequestsToWS,
  Shown,
  Subcommand,
  UI,
  View as ViewSpec,
} from "./protocol.gen.ts";
import {
  CodeInvalidParams,
  CodeMethodNotFound,
  CodeNotFound,
  MethodActionRun,
  MethodCommandRun,
  MethodConfigChanged,
  MethodInitialize,
  MethodLog,
  MethodRefShow,
  MethodShutdown,
  MethodViewBadge,
  MethodViewChanged,
  MethodViewLooking,
} from "./protocol.gen.ts";
import { Conn, methodNotFound, RPCError, Then } from "./rpc.ts";
import { actionsProblem, choose, offered, refProblem, Versions } from "./rules.ts";
import { Config } from "./config.ts";
import { Call, Command, ExitError, help, helpFor, isHelp, UsageError } from "./command.ts";
import { Act, Action, runAct } from "./action.ts";
import { Ref } from "./ref.ts";
import { stamp } from "./fields.ts";
import { View, type RawHandler, type Request } from "./view.ts";
import { NotFoundError, UnsupportedError, type Host } from "./host.ts";

/** Who's looking at what the plugin shows in ws's web UI, as ws last said. */
export interface Looking {
  /** The plugin's app is on screen, in a page of ws's that's showing. */
  app: boolean;
  /** A page of ws's web UI is showing, so the plugin's sidebar entries are on screen. True whenever app is. */
  ws: boolean;
}

export interface PluginOptions {
  /**
   * The plugin's folder, which its app (ui) and agents.md are found in:
   * where its plugin.toml is. ws starts the plugin there, so it's the
   * working directory unless said.
   */
  root?: string;
}

/** A view's entry in ws's sidebar. */
export interface ViewOptions {
  /** One of ws's icon names. */
  icon?: string;
  /** The group it's under in the sidebar. */
  under?: string;
  /** Where in the app it opens: "/" unless said. */
  at?: string;
}

/** The plugin's log: its lines go to its log file, and warnings and errors to ws as well. */
export interface Log {
  debug(message: string): void;
  info(message: string): void;
  warn(message: string): void;
  error(message: string): void;
}

type WSMethods = { requests: RequestsToWS; notifications: NotificationsToWS };

export class Plugin {
  /** The plugin's config: keys are declared on it, and read through what declaring them returns. */
  readonly config = new Config();
  readonly log: Log;
  /** How long a command or an action may run, in milliseconds. */
  commandTimeout = 2 * 60_000;

  private root: string;
  private description = "";
  private commands: Command[] = [];
  private views: ViewSpec[] = [];
  private uiDir = "";
  private uiSpec?: UI;
  private http = new View();
  private refs: Ref[] = [];
  private actions: Action[] = [];
  private setups: (() => unknown)[] = [];
  private onConfigs: (() => unknown)[] = [];
  private onLookings: ((l: Looking) => unknown)[] = [];
  private guideText = "";
  private guideSet = false;
  private roles: string[] = [];
  private errors: string[] = [];

  private started = false;
  private inSetup = false;
  private prepared?: Promise<Registration>;
  private info?: InitializeParams;
  private conn?: Conn<WSMethods>;
  private looking_: Looking = { app: false, ws: false };
  private running = new Set<Promise<unknown>>();
  private initialized: Promise<void>;
  private markInitialized!: () => void;
  private stopping?: () => void;
  private host: Host;

  constructor(opts: PluginOptions = {}) {
    this.root = resolve(opts.root ?? process.cwd());
    this.initialized = new Promise((r) => (this.markInitialized = r));
    this.config.inSetup = () => this.inSetup;
    this.config.declaring = (what) => this.declaring(what);
    const line = (level: string, message: string) => process.stderr.write(`level=${level.toUpperCase()} msg=${JSON.stringify(message)}\n`);
    const toWS = (level: "warn" | "error", message: string) => this.conn?.notify(MethodLog, { level, message });
    this.log = {
      debug: (m) => line("debug", m),
      info: (m) => line("info", m),
      warn: (m) => (line("warn", m), toWS("warn", m)),
      error: (m) => (line("error", m), toWS("error", m)),
    };
    this.http.onError = (what, err) => this.log.error(`${what} failed: ${err instanceof Error ? err.message : String(err)}`);
    this.host = { notify: (m, p) => this.conn?.notify(m, p), ask: (m, p, s) => this.ask(m, p, s) };
  }

  /** What the plugin is, in a line, for ws plugin list. */
  describe(text: string) {
    this.declaring("describe");
    this.description = text;
  }

  /**
   * Run once initialize has brought the config, before the registration's
   * given: where to declare what depends on config. When a key read in fn
   * later changes, the plugin exits and ws starts it afresh.
   */
  setup(fn: () => unknown) {
    this.declaring("setup");
    this.setups.push(fn);
  }

  /** Run after the config changes while the plugin runs: keys read the new values. */
  onConfig(fn: () => unknown) {
    this.declaring("onConfig");
    this.onConfigs.push(fn);
  }

  /** Run when who's looking changes. What a plugin fetches, and when, is its own to decide. */
  onLooking(fn: (l: Looking) => unknown) {
    this.declaring("onLooking");
    this.onLookings.push(fn);
  }

  /** Who's looking now: nobody until ws says otherwise. */
  get looking(): Looking {
    return { ...this.looking_ };
  }

  /** What agents are told of the plugin, and the roles it's for (none: every role). agents.md in the plugin's folder is its guide otherwise. */
  guide(text: string, ...roles: string[]) {
    this.declaring("guide");
    if (text) {
      this.guideText = text;
      this.guideSet = true;
    }
    this.roles = roles;
  }

  /** The plugin's app: the folder of its built files, in the plugin's folder. app.js is its entry, app.css its style. */
  ui(dir: string) {
    this.declaring("ui");
    if (this.uiDir) this.errors.push("ui is declared twice: a plugin has one app");
    this.uiDir = dir;
  }

  /** An entry in ws's sidebar, which opens the app. */
  view(title: string, opts: ViewOptions = {}) {
    this.declaring("view");
    const v: ViewSpec = { title, path: opts.at ?? "/" };
    if (opts.icon) v.icon = opts.icon;
    if (opts.under) v.under = opts.under;
    if (!v.path.startsWith("/")) this.errors.push(`the view "${title}" opens at "${v.path}": a path in the app starts with /`);
    this.views.push(v);
  }

  /** An endpoint the app asks with GET: what fn returns is answered as JSON (undefined: 204). */
  get<Out>(path: string, fn: (req: Request) => Out | Promise<Out>) {
    this.route("GET", path, false, (req) => fn(req));
  }
  /** An endpoint the app asks with DELETE. */
  delete<Out>(path: string, fn: (req: Request) => Out | Promise<Out>) {
    this.route("DELETE", path, false, (req) => fn(req));
  }
  /** An endpoint the app sends JSON with POST. */
  post<In, Out>(path: string, fn: (req: Request, body: In) => Out | Promise<Out>, opts: { body?: boolean } = {}) {
    this.route("POST", path, opts.body ?? true, fn);
  }
  /** An endpoint the app sends JSON with PUT. */
  put<In, Out>(path: string, fn: (req: Request, body: In) => Out | Promise<Out>) {
    this.route("PUT", path, true, fn);
  }
  /** An endpoint the app sends JSON with PATCH. */
  patch<In, Out>(path: string, fn: (req: Request, body: In) => Out | Promise<Out>) {
    this.route("PATCH", path, true, fn);
  }
  /** A route answered as fn likes: "GET /files/{name...}", or a path for any method. */
  handle(pattern: string, fn: RawHandler) {
    this.declaring("handle");
    const err = this.http.handle(pattern, fn);
    if (err) this.errors.push(err);
  }

  private route<In, Out>(method: string, path: string, takes: boolean, fn: (req: Request, body: In) => Out | Promise<Out>) {
    this.declaring(method.toLowerCase());
    const err = path.startsWith("/") ? this.http.endpoint(method, path, takes, fn) : `${method.toLowerCase()}("${path}"): an endpoint's path starts with /`;
    if (err) this.errors.push(err);
  }

  /** A command: ws <plugin> name [args]. */
  command(name: string, short: string, fn: (c: Call) => unknown): Command {
    this.declaring("command");
    if (this.commands.some((c) => c.name === name)) this.errors.push(`command "${name}" is declared twice`);
    const c = new Command(name, short, fn);
    this.commands.push(c);
    return c;
  }

  /** Something to do, offered in Find. */
  action(id: string, title: string, fn: (a: Act) => unknown): Action {
    this.declaring("action");
    const a = new Action({ id, title }, fn);
    this.actions.push(a);
    return a;
  }

  /** A kind of thing the plugin has that words name. */
  ref(kind: string, title: string): Ref {
    this.declaring("ref");
    if (this.refs.some((r) => r.spec.kind === kind)) this.errors.push(`the ref "${kind}" is declared twice`);
    const r = new Ref(this.host, kind, title);
    this.refs.push(r);
    return r;
  }

  /** Tells ws that what the app shows is stale: the open app fetches it again. */
  changed() {
    this.conn?.notify(MethodViewChanged, {});
  }

  /** The count beside a view's sidebar entry, by the path it opens at: 0 removes it. */
  badge(path: string, count: number) {
    this.conn?.notify(MethodViewBadge, { path, count });
  }

  /** The plugin's name, as ws knows it: known once it's running, like dir, data and workspace. */
  get name() {
    return this.info?.plugin.name ?? "";
  }
  /** The plugin's own folder, where its plugin.toml is. */
  get dir() {
    return this.info?.plugin.dir ?? "";
  }
  /** The folder for whatever the plugin keeps. ws never reads it. */
  get data() {
    return this.info?.plugin.data ?? "";
  }
  /** The workspace's folder. */
  get workspace() {
    return this.info?.workspace.root ?? "";
  }

  // What the plugin asks ws.

  /** The workspace's missions, those in play first: with archived, those archived too. */
  async missions(archived = false, signal?: AbortSignal): Promise<Mission[]> {
    return (await this.ask("mission/list", archived ? { archived } : {}, signal)).missions;
  }
  /** One mission, by its slug: with its brief and plan. */
  mission(slug: string, signal?: AbortSignal): Promise<MissionDetail> {
    return this.ask("mission/show", { slug }, signal);
  }
  /** One of the workspace's things by its ref, as ws show shows it. */
  show(ref: string, signal?: AbortSignal): Promise<Shown> {
    return this.ask("show", { ref }, signal);
  }
  /** One of a mission's own things (t3, d1…), as ws show shows it. */
  showIn(mission: string, ref: string, signal?: AbortSignal): Promise<Shown> {
    return this.ask("show", ref ? { mission, ref } : { mission }, signal);
  }
  /** Starts a conversation, for the person, with what they'd say first. */
  startConversation(message: string, signal?: AbortSignal): Promise<ConversationStarted> {
    return this.ask("conversation/start", { message }, signal);
  }
  /** The workspace's repos, in ws's order. */
  async repos(signal?: AbortSignal): Promise<Repo[]> {
    return (await this.ask("repo/list", undefined, signal)).repos;
  }

  private async ask<M extends keyof RequestsToWS>(method: M, params: RequestsToWS[M]["params"], signal?: AbortSignal): Promise<RequestsToWS[M]["result"]> {
    if (!this.conn) throw new Error(`${method}: the plugin isn't connected to ws: it's asked once run has started`);
    try {
      return (await this.conn.call(method, params as never, signal)) as RequestsToWS[M]["result"];
    } catch (err) {
      if (err instanceof RPCError && err.code === CodeMethodNotFound) throw new UnsupportedError(`ws ${this.info?.ws.version ?? ""} can't answer ${method}`);
      if (err instanceof RPCError && err.code === CodeNotFound) throw new NotFoundError(err.message);
      throw err;
    }
  }

  /** What the plugin registers, as declared: run answers initialize with it. */
  registration(): Promise<Registration> {
    this.prepared ??= this.prepare();
    return this.prepared;
  }

  private async prepare(): Promise<Registration> {
    this.started = true;
    this.inSetup = true;
    try {
      for (const fn of this.setups) await fn();
    } finally {
      this.inSetup = false;
    }
    if (!this.guideSet) this.guideText = await readFile(join(this.root, "agents.md"), "utf8").catch(() => "");
    if (this.uiDir) {
      try {
        this.uiSpec = await this.http.useUI(resolve(this.root, this.uiDir));
      } catch (err) {
        this.errors.push(`the app's entry, ${this.uiDir}/app.js, isn't there: build the app (${err instanceof Error ? err.message : String(err)})`);
      }
    } else if (this.views.length) {
      this.errors.push(`the view "${this.views[0]!.title}" has no app to show: declare the plugin's app with ui("ui/dist")`);
    }
    for (const r of this.refs) {
      const problem = refProblem(r.spec);
      if (problem) this.errors.push(`the ref "${r.spec.kind}": ${problem}`);
    }
    for (const a of this.actions) if (!a.fn) this.errors.push(`the action "${a.spec.id}" has nothing that does it`);
    const problem = actionsProblem(this.actions.map((a) => a.spec));
    if (problem) this.errors.push(problem);
    this.errors.push(...this.config.errors);
    if (this.errors.length) throw new Error(this.errors.join("\n"));
    return this.build();
  }

  private build(): Registration {
    const reg: Registration = {};
    if (this.description) reg.description = this.description;
    if (this.views.length) reg.views = this.views;
    if (this.uiSpec) reg.ui = this.uiSpec;
    if (this.commands.length) {
      reg.commands = {
        name: this.name,
        short: this.description,
        subcommands: this.commands.map((c) => {
          const s: Subcommand = { name: c.name };
          if (c.short) s.short = c.short;
          if (c.usage) s.args = c.usage;
          if (c.conversation) s.conversations = true;
          if (c.label_) s.label = c.label_;
          const options = c.flags.filter((f) => f.takesValue).map((f) => f.name);
          if (options.length) s.options = options;
          return s;
        }),
      };
      if (!reg.commands.name) delete reg.commands.name;
      if (!reg.commands.short) delete reg.commands.short;
    }
    if (this.guideText) reg.agent = this.roles.length ? { roles: this.roles, guide: this.guideText } : { guide: this.guideText };
    if (this.refs.length) reg.refs = this.refs.map((r) => r.spec);
    if (this.actions.length) reg.actions = this.actions.map((a) => a.spec);
    if (this.config.version) reg.config_version = this.config.version;
    const config = this.config.spec();
    if (config) reg.config = config;
    return reg;
  }

  private declaring(what: string) {
    if (this.started && !this.inSetup) throw new Error(`plugin: ${what} after run has started: declare everything before run, or in setup`);
  }

  /**
   * Speaks the protocol on stdin and stdout until ws says to stop, then
   * exits. With --registration as its only argument, it prints what the
   * plugin would register, and exits.
   */
  run(): void {
    if (process.argv.length === 3 && process.argv[2] === "--registration") {
      this.registration().then(
        (reg) => {
          process.stdout.write(JSON.stringify(reg, null, 2) + "\n");
          process.exit(0);
        },
        (err) => {
          process.stderr.write(`${err instanceof Error ? err.message : String(err)}\n`);
          process.exit(1);
        },
      );
      return;
    }
    // Stdout is the connection's alone: what the plugin's own code prints
    // goes to its log instead.
    const write = process.stdout.write.bind(process.stdout);
    const out = new Writable({ write: (chunk, enc, cb) => (write(chunk, enc as BufferEncoding), cb()) });
    process.stdout.write = process.stderr.write.bind(process.stderr) as typeof process.stdout.write;
    this.serve(process.stdin, out).then(
      () => process.exit(0),
      (err) => {
        process.stderr.write(`plugin: ${err instanceof Error ? err.message : String(err)}\n`);
        process.exit(1);
      },
    );
  }

  /** Speaks the protocol on input and output until the other side says to stop, or input ends. A test serves on streams of its own. */
  serve(input: Readable, output: Writable): Promise<void> {
    const conn = new Conn<WSMethods>(input, output, (m, p, s) => this.dispatch(m, p, s));
    this.conn = conn;
    return new Promise<void>((resolve) => {
      this.stopping = resolve;
      void conn.done.then(() => resolve());
    }).finally(() => this.http.close());
  }

  private exit() {
    this.stopping?.();
  }

  private track<T>(p: Promise<T>): Promise<T> {
    this.running.add(p);
    void p.finally(() => this.running.delete(p)).catch(() => {});
    return p;
  }

  private async dispatch(method: string, params: unknown, signal: AbortSignal): Promise<unknown> {
    switch (method) {
      case MethodInitialize:
        return this.initialize(params as InitializeParams);
      case MethodConfigChanged:
        return this.configChanged((params as { config?: Record<string, unknown> })?.config);
      case MethodViewLooking: {
        await this.initialized;
        const l = params as Looking;
        return this.looked({ app: !!l.app, ws: !!l.ws || !!l.app });
      }
      case MethodCommandRun:
        await this.initialized;
        return { exit: await this.track(this.runCommand(params as CommandRunParams, signal)) };
      case MethodRefShow:
        await this.initialized;
        return this.track(this.showRef(params as RefShowParams, signal));
      case MethodActionRun:
        await this.initialized;
        return this.track(this.runAction(params as ActionRunParams, signal));
      case MethodShutdown: {
        // What's being run gets a moment to finish and be answered.
        await Promise.race([Promise.allSettled([...this.running]), new Promise((r) => setTimeout(r, 3000))]);
        return new Then(null, () => this.exit());
      }
    }
    throw methodNotFound(method);
  }

  private async initialize(params: InitializeParams): Promise<unknown> {
    if (this.info) throw new Error("initialize was already sent");
    this.info = params;
    const chosen = choose(Versions, offered(params));
    if (chosen === 0) {
      this.markInitialized();
      this.log.error(`ws speaks versions ${JSON.stringify(offered(params))} of the plugin protocol, and this plugin ${JSON.stringify(Versions)}: none in common`);
      return { protocol: 0, protocols: [...Versions], registration: {} } satisfies InitializeResult;
    }
    try {
      this.config.set(params.config);
      const registration = await this.registration();
      if (this.http.hasRoutes && params.view?.listen) await this.http.listen(params.view.listen);
      return { protocol: chosen, protocols: [...Versions], registration } satisfies InitializeResult;
    } finally {
      this.markInitialized();
    }
  }

  private async configChanged(values: Record<string, unknown> | undefined) {
    if (this.config.set(values)) {
      this.log.info("config that the registration depends on changed: exiting to start afresh");
      this.exit();
      return;
    }
    for (const fn of this.onConfigs) await this.guard("onConfig", fn);
    this.changed();
  }

  private async looked(l: Looking) {
    const was = this.looking_;
    this.looking_ = l;
    if (was.app === l.app && was.ws === l.ws) return;
    for (const fn of this.onLookings) await this.guard("onLooking", () => fn({ ...l }));
  }

  private async guard(what: string, fn: () => unknown) {
    try {
      await fn();
    } catch (err) {
      this.log.error(`${what} failed: ${err instanceof Error ? err.message : String(err)}`);
    }
  }

  private async runCommand(run: CommandRunParams, signal: AbortSignal): Promise<number> {
    const out = (stream: "stdout" | "stderr", data: string) => data && this.conn?.notify("command/output", { id: run.id, stream, data });
    const args = run.args ?? [];
    if (args.length === 0 || isHelp(args[0])) {
      out("stdout", help(this.name, this.description, this.commands));
      return 0;
    }
    const cmd = this.commands.find((c) => c.name === args[0]);
    if (!cmd) {
      out("stderr", `unknown command "${args[0]}"\n\n` + help(this.name, this.description, this.commands));
      return 2;
    }
    const call = new Call(this.host, cmd, run);
    let wantsHelp: boolean;
    try {
      wantsHelp = call.parse(args.slice(1));
    } catch (err) {
      if (!(err instanceof UsageError)) throw err;
      out("stderr", `${err.message}\n\n` + helpFor(this.name, cmd));
      return 2;
    }
    if (wantsHelp) {
      out("stdout", helpFor(this.name, cmd));
      return 0;
    }
    const limit = cmd.timeoutMs ?? this.commandTimeout;
    const timeout = limit > 0 ? AbortSignal.timeout(limit) : undefined;
    call.signal = timeout ? AbortSignal.any([signal, timeout]) : signal;
    try {
      await cmd.fn(call);
      return 0;
    } catch (err) {
      let message = err instanceof Error ? err.message : String(err);
      if (timeout?.aborted) message = `${cmd.name} ran out of time (${duration(limit)})`;
      out("stderr", message + "\n");
      return err instanceof ExitError ? err.code : 1;
    }
  }

  private async showRef(params: RefShowParams, signal: AbortSignal): Promise<RefShowResult> {
    const ref = this.refs.find((r) => r.spec.kind === params.kind);
    if (!ref) throw new RPCError(CodeInvalidParams, `this plugin has no ref called "${params.kind}"`);
    if (!ref.showFn) throw methodNotFound(MethodRefShow);
    try {
      const thing = await ref.showFn({ name: params.ref, full: !!params.full }, AbortSignal.any([signal, AbortSignal.timeout(this.commandTimeout)]));
      const result: RefShowResult = { fields: thing.fields ?? {} };
      if (thing.changed) result.changed = stamp(thing.changed);
      return result;
    } catch (err) {
      if (err instanceof NotFoundError) throw new RPCError(CodeNotFound, `there's no ${params.kind} ${params.ref}`);
      throw err;
    }
  }

  private async runAction(params: ActionRunParams, signal: AbortSignal) {
    const fn = params.kind ? this.refs.find((r) => r.spec.kind === params.kind)?.acts.get(params.id) : this.actions.find((a) => a.spec.id === params.id)?.fn;
    if (!fn) throw new RPCError(CodeInvalidParams, `this plugin has no action called "${params.id}"`);
    const act = new Act(params.ref ?? "", AbortSignal.any([signal, AbortSignal.timeout(this.commandTimeout)]), params.answers ?? []);
    return runAct(fn, act);
  }
}

function duration(ms: number): string {
  const s = Math.round(ms / 1000);
  const m = Math.floor(s / 60);
  return m ? `${m}m${s % 60}s` : `${s}s`;
}
