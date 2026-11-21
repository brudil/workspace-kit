// Actions: things the plugin does when it's asked from Find, its own or on
// one of its things. One that asks something is run again from the start
// with each answer, so it asks before it does anything.

import type { Action as ActionSpec, ActionRunResult, Ask, Option } from "./protocol.gen.ts";
import { AskPick, AskText } from "./protocol.gen.ts";

/** An action, as declared. */
export class Action {
  /** @internal */ readonly spec: ActionSpec;
  /** @internal */ readonly fn: (a: Act) => unknown;
  constructor(spec: ActionSpec, fn: (a: Act) => unknown) {
    this.spec = spec;
    this.fn = fn;
  }

  /** One of ws's icon names. */
  icon(name: string): this {
    this.spec.icon = name;
    return this;
  }

  /** More, after its title. */
  hint(text: string): this {
    this.spec.hint = text;
    return this;
  }
}

const asking = Symbol("the action is asking");

/** A choice to pick: what's answered, and what's shown. */
export type Choice = Option;

/** Choices whose labels are what's answered. */
export const choices = (...labels: string[]): Choice[] => labels.map((l) => ({ value: l, label: l }));

/** One run of an action. */
export class Act {
  /** The thing it's on (RAM-12), for one of a kind's: "" for the plugin's own. */
  readonly name: string;
  /** Aborts when it's run out of time, or ws gave up on it. */
  readonly signal: AbortSignal;
  /** @internal */ out: ActionRunResult = {};
  private asked = 0;
  private answers: string[];

  constructor(name: string, signal: AbortSignal, answers: string[]) {
    this.name = name;
    this.signal = signal;
    this.answers = answers;
  }

  /** Asks for a line typed. */
  ask(title: string, placeholder?: string): Promise<string> {
    const q: Ask = { title, type: AskText };
    if (placeholder) q.placeholder = placeholder;
    return this.question(q);
  }

  /** Asks for one of choices. */
  pick(title: string, ...options: Choice[]): Promise<string> {
    return this.question({ title, type: AskPick, options });
  }

  private question(q: Ask): Promise<string> {
    if (this.asked < this.answers.length) return Promise.resolve(this.answers[this.asked++]!);
    this.out.ask = q;
    return Promise.reject(asking);
  }

  /** A line to tell whoever asked. */
  say(text: string) {
    this.out.say = text;
  }

  /** A path in the plugin's app to go to. */
  open(path: string) {
    this.out.open = path;
  }
}

/** @internal Runs an action: what came of it, or what it asks. */
export async function runAct(fn: (a: Act) => unknown, act: Act): Promise<ActionRunResult> {
  try {
    await fn(act);
  } catch (err) {
    if (err === asking && act.out.ask) return { ask: act.out.ask };
    throw err;
  }
  delete act.out.ask;
  return act.out;
}
