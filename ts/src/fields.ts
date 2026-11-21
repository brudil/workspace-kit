// The fields a kind's things are made of (Ref.fields): each a name, in
// lower-case letters, digits and underscores, and what it is.

import type { Field } from "./protocol.gen.ts";
import { FieldMarkdown, FieldRef, FieldRefs, FieldState, FieldText, FieldTime, FieldTitle } from "./protocol.gen.ts";

const make = (name: string, type: string, label?: string, link?: string): Field => {
  const f: Field = { name, type };
  if (label) f.label = label;
  if (link) f.link = link;
  return f;
};

/** What a thing's called: a line. A kind has one. */
export const title = (name: string) => make(name, FieldTitle);
/** Where it stands, in the plugin's words: at most one. */
export const state = (name: string, label?: string) => make(name, FieldState, label);
/** Words: a name, a number, a few of them. */
export const text = (name: string, label?: string) => make(name, FieldText, label);
/** A moment: its value is RFC 3339 (stamp). */
export const time = (name: string, label?: string) => make(name, FieldTime, label);
/** What it says, at length. */
export const markdown = (name: string, label?: string) => make(name, FieldMarkdown, label);
/** One name of another thing, linked in ws's graph as how says (LinkIn, LinkAbout…; LinkMentions by default). */
export const names = (name: string, label: string, how?: string) => make(name, FieldRef, label, how);
/** Several names, separated by spaces, commas or lines. */
export const namesSeveral = (name: string, label: string, how?: string) => make(name, FieldRefs, label, how);

/** A moment as a time field's value: RFC 3339, in UTC, to the second. "" for none. */
export function stamp(d: Date | undefined): string {
  if (!d || isNaN(d.getTime())) return "";
  return d.toISOString().replace(/\.\d{3}Z$/, "Z");
}
