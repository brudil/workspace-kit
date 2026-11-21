// One side of a JSON-RPC 2.0 connection over a pair of streams, one message
// per line: what a plugin and ws speak. The framework and its tests use it.

import type { Readable, Writable } from "node:stream";
import { createInterface } from "node:readline";
import { CodeCancelled, CodeInternal, CodeMethodNotFound, MethodCancelRequest } from "./protocol.gen.ts";

type ID = string | number;

interface Message {
  jsonrpc: "2.0";
  id?: ID;
  method?: string;
  params?: unknown;
  result?: unknown;
  error?: { code: number; message: string; data?: unknown };
}

/** A JSON-RPC error: what a request is answered with when it fails. */
export class RPCError extends Error {
  readonly code: number;
  readonly data?: unknown;
  constructor(code: number, message: string, data?: unknown) {
    super(message);
    this.name = "RPCError";
    this.code = code;
    this.data = data;
  }
}

/** The answer to a request nobody handles. */
export const methodNotFound = (method: string) => new RPCError(CodeMethodNotFound, `method not found: ${method}`);

/** A request's answer once the connection's gone. */
export class ClosedError extends Error {
  constructor() {
    super("the connection closed");
    this.name = "ClosedError";
  }
}

/**
 * Answers what the other side sends. A request's answer is what it
 * returns (or throws); a notification's is dropped. signal aborts when the
 * connection closes, or when the other side gives up on the request
 * ($/cancel_request).
 */
export type Handler = (method: string, params: unknown, signal: AbortSignal) => unknown;

/**
 * A request's result with something to do once it's been sent: a handler
 * returns it to act only after the other side has its answer.
 */
export class Then {
  // Not "then": an object with one is a promise to await.
  readonly result: unknown;
  readonly after: () => void;
  constructor(result: unknown, after: () => void) {
    this.result = result;
    this.after = after;
  }
}

/** The methods one side may ask and tell the other: params, and results. */
export interface Methods {
  requests: object;
  notifications: object;
}

type Params<T, M> = M extends keyof T ? (T[M] extends { params: infer P } ? P : never) : never;
type Result<T, M> = M extends keyof T ? (T[M] extends { result: infer R } ? R : never) : never;

/**
 * One side of a connection. Out is what this side sends: its requests and
 * notifications, typed by method.
 */
export class Conn<Out extends Methods> {
  private nextID = 0;
  private waiting = new Map<string, { resolve: (m: Message) => void; reject: (e: Error) => void }>();
  private serving = new Map<string, AbortController>();
  private ended = false;
  private life = new AbortController();
  // Notifications are handled in the order they came, each before what
  // follows it: queue is the last one's.
  private queue: Promise<void> = Promise.resolve();
  /** Resolves once the other side's stream has ended. */
  readonly done: Promise<void>;

  private output: Writable;
  private handler: Handler;

  constructor(input: Readable, output: Writable, handler: Handler) {
    this.output = output;
    this.handler = handler;
    const lines = createInterface({ input, crlfDelay: Infinity });
    lines.on("line", (line) => this.read(line));
    this.done = new Promise((resolve) => lines.on("close", () => this.close().then(resolve)));
  }

  private read(line: string) {
    if (!line.trim()) return;
    let m: Message;
    try {
      m = JSON.parse(line);
    } catch {
      return; // not a message: skip it
    }
    if (typeof m !== "object" || m === null) return;
    if (m.method === MethodCancelRequest) {
      // Handled here, not queued: the request it's about may be what
      // everything queued is waiting behind.
      const id = (m.params as { requestId?: ID } | undefined)?.requestId;
      if (id !== undefined) this.serving.get(key(id))?.abort();
      return;
    }
    if (m.method !== undefined) {
      if (m.id !== undefined) {
        // Registered as it's read, so a cancel that follows at once finds it.
        const ac = new AbortController();
        this.life.signal.addEventListener("abort", () => ac.abort(), { once: true });
        this.serving.set(key(m.id), ac);
        this.enqueue(async () => {
          void this.answer(m, ac.signal); // its own task, once what came before it's handled
        });
      } else {
        this.enqueue(async () => {
          try {
            await this.handler(m.method!, m.params, this.life.signal);
          } catch {
            // a notification has no answer
          }
        });
      }
      return;
    }
    if (m.id !== undefined) {
      this.enqueue(async () => {
        const w = this.waiting.get(key(m.id!));
        this.waiting.delete(key(m.id!));
        w?.resolve(m);
      });
    }
  }

  private enqueue(fn: () => Promise<void>) {
    this.queue = this.queue.then(fn, fn);
  }

  private async answer(m: Message, signal: AbortSignal) {
    let reply: Message = { jsonrpc: "2.0", id: m.id };
    let then: (() => void) | undefined;
    try {
      let result = await this.handler(m.method!, m.params, signal);
      if (result instanceof Then) {
        then = result.after;
        result = result.result;
      }
      reply.result = result ?? null;
    } catch (err) {
      if (err instanceof RPCError) reply.error = { code: err.code, message: err.message, data: err.data };
      else if (signal.aborted) reply.error = { code: CodeCancelled, message: "Request cancelled" };
      else reply.error = { code: CodeInternal, message: err instanceof Error ? err.message : String(err) };
    }
    this.serving.delete(key(m.id!));
    this.write(reply);
    then?.();
  }

  private write(m: Message) {
    if (this.ended) return;
    try {
      this.output.write(JSON.stringify(m) + "\n");
    } catch {
      // the other side has gone
    }
  }

  /**
   * Sends a request and waits for its answer. If signal aborts first, it
   * tells the other side it's given up ($/cancel_request) and rejects.
   */
  call<M extends keyof Out["requests"] & string>(
    method: M,
    params: Params<Out["requests"], M>,
    signal?: AbortSignal,
  ): Promise<Result<Out["requests"], M>> {
    if (this.ended) return Promise.reject(new ClosedError());
    const id = ++this.nextID;
    return new Promise((resolve, reject) => {
      const onAbort = () => {
        this.waiting.delete(key(id));
        this.write({ jsonrpc: "2.0", method: MethodCancelRequest, params: { requestId: id } });
        reject(signal!.reason ?? new Error("aborted"));
      };
      if (signal?.aborted) return onAbort();
      signal?.addEventListener("abort", onAbort, { once: true });
      this.waiting.set(key(id), {
        resolve: (m) => {
          signal?.removeEventListener("abort", onAbort);
          if (m.error) reject(new RPCError(m.error.code, m.error.message, m.error.data));
          else resolve(m.result as Result<Out["requests"], M>);
        },
        reject,
      });
      const message: Message = { jsonrpc: "2.0", id, method };
      if (params !== undefined) message.params = params;
      this.write(message);
    });
  }

  /** Sends a notification: no answer. */
  notify<M extends keyof Out["notifications"] & string>(method: M, params: Out["notifications"][M & keyof Out["notifications"]]) {
    this.write({ jsonrpc: "2.0", method, params });
  }

  /** Whether the other side's stream has ended. */
  get closed() {
    return this.ended;
  }

  private async close() {
    await this.queue;
    this.ended = true;
    for (const w of this.waiting.values()) w.reject(new ClosedError());
    this.waiting.clear();
    this.life.abort();
  }
}

const key = (id: ID) => JSON.stringify(id);
