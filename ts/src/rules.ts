// What the protocol's types are held to, as the Go kit's protocol/rules.go
// holds them: version choice, and what makes a registration sound.

import type { Action, InitializeParams, Ref } from "./protocol.gen.ts";
import { FieldKey, FieldState, FieldTitle, RefPlaceholder, TailNumber } from "./protocol.gen.ts";

/**
 * Every version of the protocol this kit speaks, oldest first. Within a
 * version, changes only add.
 */
export const Versions: readonly number[] = [1];

/** The version a handshake settles on: the highest both speak, or 0 for none. */
export function choose(a: readonly number[], b: readonly number[]): number {
  let chosen = 0;
  for (const v of a) if (v > chosen && b.includes(v)) chosen = v;
  return chosen;
}

/** The versions ws speaks, from what it said. A ws that says nothing of it speaks 1. */
export function offered(p: InitializeParams): number[] {
  if (p.protocols?.length) return p.protocols;
  if (p.protocol > 0) return [p.protocol];
  return [1];
}

const actionID = /^[a-z][a-z0-9-]{0,39}$/;
const refKind = /^[a-z][a-z0-9-]{0,39}$/;
const refPrefix = /^[A-Za-z][A-Za-z0-9_]{0,30}-$/;
const refField = /^[a-z][a-z0-9_]{0,39}$/;
const tailNumber = /^[1-9][0-9]{0,8}$/;

/** What's wrong with a list of actions as registered, or "". */
export function actionsProblem(actions: readonly Action[]): string {
  const seen = new Set<string>();
  for (const a of actions) {
    if (!actionID.test(a.id)) return `its action "${a.id}" must be lower-case letters, digits and hyphens, starting with a letter`;
    if (seen.has(a.id)) return `its action "${a.id}" is there twice`;
    if (!a.title.trim()) return `its action "${a.id}" has no title: it's what Find calls it`;
    seen.add(a.id);
  }
  return "";
}

/** Whether name is one of the kind's: one of its prefixes and its tail, whole and as written. */
export function names(r: Ref, name: string): boolean {
  if (r.tail && r.tail !== TailNumber) return false;
  return r.prefixes.some((p) => name.startsWith(p) && tailNumber.test(name.slice(p.length)));
}

/**
 * What's wrong with a ref as it's registered, or "". The Go kit's
 * Ref.Problem, but for remotes, which this kit doesn't declare yet.
 */
export function refProblem(r: Ref): string {
  if (!refKind.test(r.kind)) return `its kind "${r.kind}" must be lower-case letters, digits and hyphens, starting with a letter`;
  if (r.prefixes.length === 0) return "it has no prefixes: nothing would be named by it";
  for (const p of r.prefixes) {
    if (!refPrefix.test(p)) return `its prefix "${p}" must be letters, digits and underscores, starting with a letter and ending in a hyphen (RAM-)`;
  }
  if (r.address) {
    let ok = false;
    try {
      const u = new URL(r.address.replace(RefPlaceholder, "X-1"));
      ok = (u.protocol === "http:" || u.protocol === "https:") && !!u.host && count(r.address, RefPlaceholder) === 1;
      if (ok && (u.host.includes("X-1") || /\s/.test(r.address))) return `its address "${r.address}" must have ${RefPlaceholder} after its host`;
    } catch {
      ok = false;
    }
    if (!ok) return `its address "${r.address}" must be an http or https address with ${RefPlaceholder} once, where the name goes`;
  }
  if (r.path && (!r.path.startsWith("/") || r.path.startsWith("//") || count(r.path, RefPlaceholder) !== 1)) {
    return `its path "${r.path}" must be a path in the plugin's app (/issue/${RefPlaceholder}) with ${RefPlaceholder} once`;
  }
  const has = new Set([FieldKey]);
  let titles = 0;
  let states = 0;
  for (const f of r.fields ?? []) {
    if (f.name === FieldKey) return `its field "${FieldKey}" is every thing's own: its name`;
    if (!refField.test(f.name)) return `its field "${f.name}" must be lower-case letters, digits and underscores, starting with a letter`;
    if (has.has(f.name)) return `its field "${f.name}" is there twice`;
    has.add(f.name);
    if (f.type === FieldTitle) titles++;
    if (f.type === FieldState) states++;
    if (!f.type) return `its field "${f.name}" has no type`;
  }
  if ((r.fields?.length ?? 0) > 0 && titles !== 1) return `it has ${titles} fields of type ${FieldTitle}: a thing has one thing it's called`;
  if (states > 1) return `it has ${states} fields of type ${FieldState}: a thing stands in one place`;
  for (const s of r.search ?? []) {
    if (!has.has(s.field) || (s.max ?? 0) < 0) return `it's found by "${s.field}", which isn't one of its fields`;
  }
  for (const [where, blocks] of Object.entries(r.views ?? {})) {
    for (const b of blocks) if (!has.has(b.field)) return `its view ${where} shows "${b.field}", which isn't one of its fields`;
  }
  return actionsProblem(r.actions ?? []);
}

const count = (s: string, sub: string) => s.split(sub).length - 1;
