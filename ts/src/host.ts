// What the framework's parts reach ws through: the plugin's connection,
// typed by what a plugin may send.

import type { NotificationsToWS, RequestsToWS } from "./protocol.gen.ts";

export interface Host {
  notify<M extends keyof NotificationsToWS>(method: M, params: NotificationsToWS[M]): void;
  ask<M extends keyof RequestsToWS>(method: M, params: RequestsToWS[M]["params"], signal?: AbortSignal): Promise<RequestsToWS[M]["result"]>;
}

/** Something asked for by name that isn't there: a 404 from an endpoint, CodeNotFound to ws. */
export class NotFoundError extends Error {
  constructor(message = "not found") {
    super(message);
    this.name = "NotFoundError";
  }
}

/** What this ws can't answer: it's from before the method was added. */
export class UnsupportedError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "UnsupportedError";
  }
}
