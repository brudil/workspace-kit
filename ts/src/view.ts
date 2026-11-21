// The plugin's own HTTP, on the unix socket ws gives it: its app's built
// files (/ui/…) and what its app asks it (endpoints). ws serves it under
// the plugin's page; the plugin serves no HTML.

import { createHash } from "node:crypto";
import { readFile, unlink } from "node:fs/promises";
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import { extname, join, normalize } from "node:path";
import type { UI } from "./protocol.gen.ts";
import { NotFoundError } from "./host.ts";

/** An endpoint's request: the HTTP request, the pattern's wildcards, and the URL's query. */
export interface Request {
  readonly http: IncomingMessage;
  /** The pattern's wildcards, by name: /issue/{key} gives params.key. */
  readonly params: Record<string, string>;
  readonly query: URLSearchParams;
  /** Aborts when the app has gone. */
  readonly signal: AbortSignal;
}

/** An error an endpoint answers with its own HTTP status. */
export class StatusError extends Error {
  readonly status: number;
  constructor(status: number, message: string) {
    super(message);
    this.name = "StatusError";
    this.status = status;
  }
}

/** What answers a pattern: its own response, written as it likes. */
export type RawHandler = (req: Request, res: ServerResponse) => unknown;

interface Route {
  method: string;
  segments: string[];
  pattern: string;
  handler: RawHandler;
}

/** @internal The plugin's routes, and its app's files: what its HTTP serves. */
export class View {
  private routes: Route[] = [];
  private files?: { dir: string; ui: UI };
  private server?: Server;
  onError: (what: string, err: unknown) => void = () => {};

  /** A route, as "GET /api/issue/{key}" or "/healthz" (any method). */
  handle(pattern: string, handler: RawHandler): string | undefined {
    const space = pattern.indexOf(" ");
    const method = space < 0 ? "" : pattern.slice(0, space);
    const path = space < 0 ? pattern : pattern.slice(space + 1);
    if (!path.startsWith("/")) return `the route "${pattern}": a path starts with /`;
    if (this.routes.some((r) => r.pattern === pattern)) return `the route "${pattern}" is declared twice`;
    this.routes.push({ method, segments: path.split("/").slice(1), pattern, handler });
    return undefined;
  }

  /** An endpoint: what it takes as JSON (for a method with a body), and what it answers as JSON. */
  endpoint<In, Out>(method: string, path: string, takes: boolean, fn: (req: Request, body: In) => Out | Promise<Out>): string | undefined {
    const pattern = `${method} ${path}`;
    return this.handle(pattern, async (req, res) => {
      let body = undefined as In;
      if (takes) {
        const text = await readBody(req.http);
        try {
          body = JSON.parse(text);
        } catch (err) {
          const why = text.trim() === "" ? "it was sent no body" : err instanceof Error ? err.message : String(err);
          return send(res, 400, "text/plain; charset=utf-8", `${pattern} takes JSON: ${why}\n`);
        }
      }
      let out: Out;
      try {
        out = await fn(req, body);
      } catch (err) {
        const status = err instanceof StatusError && err.status >= 400 && err.status <= 599 ? err.status : err instanceof NotFoundError ? 404 : 500;
        return send(res, status, "text/plain; charset=utf-8", `${err instanceof Error ? err.message : String(err)}\n`);
      }
      if (out === undefined) {
        res.writeHead(204).end();
        return;
      }
      send(res, 200, "application/json", JSON.stringify(out) + "\n");
    });
  }

  /** The app: the folder of its built files, app.js its entry and app.css its style. */
  async useUI(dir: string): Promise<UI> {
    const ui: UI = { script: await versioned(dir, "app.js") };
    try {
      ui.style = await versioned(dir, "app.css");
    } catch {
      // an app with no style
    }
    this.files = { dir, ui };
    return ui;
  }

  get hasRoutes() {
    return this.routes.length > 0 || !!this.files;
  }

  /** Serves on the unix socket at path, until close. */
  async listen(path: string) {
    await unlink(path).catch(() => {});
    const server = createServer((req, res) => void this.serve(req, res));
    await new Promise<void>((resolve, reject) => {
      server.once("error", reject);
      server.listen(path, () => resolve());
    });
    this.server = server;
  }

  close() {
    this.server?.close();
    this.server?.closeAllConnections();
  }

  private async serve(http: IncomingMessage, res: ServerResponse) {
    const url = new URL(http.url ?? "/", "http://plugin");
    const path = url.pathname;
    const clean = normalize(path).replaceAll("\\", "/");
    if (clean !== path && clean + "/" !== path) return send(res, 404, "text/plain; charset=utf-8", "404 page not found\n");
    const ac = new AbortController();
    res.on("close", () => ac.abort());

    if (this.files && path.startsWith("/ui/") && (http.method === "GET" || http.method === "HEAD")) {
      return this.serveFile(path.slice("/ui/".length), url, res);
    }
    const parts = path.split("/").slice(1);
    let allowed = false;
    for (const r of this.routes) {
      const params = match(r.segments, parts);
      if (!params) continue;
      if (r.method && r.method !== http.method && !(r.method === "GET" && http.method === "HEAD")) {
        allowed = true;
        continue;
      }
      try {
        await r.handler({ http, params, query: url.searchParams, signal: ac.signal }, res);
      } catch (err) {
        this.onError(r.pattern, err);
        if (!res.headersSent) send(res, 500, "text/plain; charset=utf-8", `${r.pattern} failed: ${err instanceof Error ? err.message : String(err)}\n`);
      }
      return;
    }
    if (allowed) return send(res, 405, "text/plain; charset=utf-8", "Method Not Allowed\n");
    send(res, 404, "text/plain; charset=utf-8", "404 page not found\n");
  }

  private async serveFile(name: string, url: URL, res: ServerResponse) {
    if (!name || name.split("/").some((p) => p === "" || p === "." || p === "..")) return send(res, 404, "text/plain; charset=utf-8", "404 page not found\n");
    let data: Buffer;
    try {
      data = await readFile(join(this.files!.dir, name));
    } catch {
      return send(res, 404, "text/plain; charset=utf-8", "404 page not found\n");
    }
    res.setHeader("Cache-Control", url.searchParams.has("v") ? "public, max-age=31536000, immutable" : "no-cache");
    send(res, 200, types[extname(name)] ?? "application/octet-stream", data);
  }
}

const types: Record<string, string> = {
  ".js": "text/javascript; charset=utf-8",
  ".mjs": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".woff2": "font/woff2",
  ".map": "application/json",
};

async function versioned(dir: string, name: string): Promise<string> {
  const data = await readFile(join(dir, name));
  return `/ui/${name}?v=${createHash("sha256").update(data).digest("hex").slice(0, 8)}`;
}

function match(pattern: string[], parts: string[]): Record<string, string> | undefined {
  const params: Record<string, string> = {};
  for (let i = 0; i < pattern.length; i++) {
    const seg = pattern[i]!;
    const wild = /^\{(\w+)(\.\.\.)?\}$/.exec(seg);
    if (wild?.[2]) {
      params[wild[1]!] = parts.slice(i).map(decodeURIComponent).join("/");
      return params;
    }
    const part = parts[i];
    if (part === undefined) return undefined;
    if (wild) params[wild[1]!] = decodeURIComponent(part);
    else if (seg !== part) return undefined;
  }
  return parts.length === pattern.length ? params : undefined;
}

function send(res: ServerResponse, status: number, type: string, body: string | Buffer) {
  res.writeHead(status, { "Content-Type": type, "Content-Length": Buffer.byteLength(body) });
  res.end(body);
}

function readBody(req: IncomingMessage): Promise<string> {
  return new Promise((resolve, reject) => {
    const parts: Buffer[] = [];
    req.on("data", (c: Buffer) => parts.push(c));
    req.on("end", () => resolve(Buffer.concat(parts).toString("utf8")));
    req.on("error", reject);
  });
}
