// A plugin's config: keys declared, and read through what declaring them
// returns. ws keeps the values and sends them; Settings draws the fields.

import type { ConfigKey } from "./protocol.gen.ts";
import { ScopeLocal, TypeBool, TypeEnum, TypeList, TypeMap, TypeNumber, TypeSecret, TypeString, TypeURL } from "./protocol.gen.ts";

/** How a key is declared, beyond its name, title and type. */
export interface KeyOptions<T> {
  /** Its value where none is set. */
  default?: T;
  /** Settings asks for one before anything works. */
  required?: boolean;
  /** Kept on this machine alone, not in the workspace's shared config. */
  local?: boolean;
  /** A line on what it's for, under the field. */
  help?: string;
  /** What a value looks like, in the field before one's typed. */
  example?: string;
}

/** A declared key: its value now, or its default. */
export class Key<T> {
  /** @internal */
  readInSetup = false;
  /** @internal */ readonly spec: ConfigKey;
  private config: Config;
  private read: (v: unknown) => T | undefined;
  private fallback: T;

  constructor(config: Config, spec: ConfigKey, read: (v: unknown) => T | undefined, fallback: T) {
    this.config = config;
    this.spec = spec;
    this.read = read;
    this.fallback = fallback;
  }

  /** Whether a value is set: not blank, nor absent. */
  get isSet(): boolean {
    return !blank(this.config.raw(this));
  }

  /** The value, or the key's default where none is set. */
  get(): T {
    const v = this.config.raw(this);
    if (!blank(v)) {
      const got = this.read(v);
      if (got !== undefined) return got;
    }
    return (this.spec.default as T | undefined) ?? this.fallback;
  }
}

export class Config {
  private keys: Key<unknown>[] = [];
  private values: Record<string, unknown> = {};
  /** @internal */ version = 0;
  /** @internal */ inSetup = () => false;
  /** @internal */ declaring: (what: string) => void = () => {};
  /** @internal */ errors: string[] = [];

  private add<T>(key: string, title: string, type: string, opts: KeyOptions<T>, read: (v: unknown) => T | undefined, fallback: T, extra: Partial<ConfigKey> = {}): Key<T> {
    this.declaring("config");
    const spec: ConfigKey = { key, title, type, ...extra };
    if (opts.default !== undefined) spec.default = opts.default as ConfigKey["default"];
    if (opts.required) spec.required = true;
    if (opts.local || type === TypeSecret) spec.scope = ScopeLocal;
    if (opts.help) spec.help = opts.help;
    if (opts.example) spec.example = opts.example;
    if (this.keys.some((k) => k.spec.key === key)) this.errors.push(`config key "${key}" is declared twice`);
    const k = new Key<T>(this, spec, read, fallback);
    this.keys.push(k as Key<unknown>);
    return k;
  }

  /** The config's version: raise it when what a key means changes, so ws asks again. */
  setVersion(n: number) {
    this.declaring("config.setVersion");
    this.version = n;
  }

  string(key: string, title: string, opts: KeyOptions<string> = {}) {
    return this.add(key, title, TypeString, opts, text, "");
  }
  url(key: string, title: string, opts: KeyOptions<string> = {}) {
    return this.add(key, title, TypeURL, opts, text, "");
  }
  secret(key: string, title: string, opts: KeyOptions<string> = {}) {
    return this.add(key, title, TypeSecret, opts, text, "");
  }
  enum(key: string, title: string, options: string[], opts: KeyOptions<string> = {}) {
    return this.add(key, title, TypeEnum, opts, text, "", { options });
  }
  list(key: string, title: string, opts: KeyOptions<string[]> = {}) {
    return this.add(key, title, TypeList, opts, (v) => (Array.isArray(v) && v.every((s) => typeof s === "string") ? v : undefined), []);
  }
  bool(key: string, title: string, opts: KeyOptions<boolean> = {}) {
    return this.add(key, title, TypeBool, opts, (v) => (typeof v === "boolean" ? v : undefined), false);
  }
  number(key: string, title: string, opts: KeyOptions<number> = {}) {
    return this.add(key, title, TypeNumber, opts, (v) => (typeof v === "number" ? v : typeof v === "string" && v.trim() !== "" && !isNaN(Number(v)) ? Number(v) : undefined), 0);
  }
  map(key: string, title: string, opts: KeyOptions<Record<string, string>> = {}) {
    return this.add(key, title, TypeMap, opts, (v) => (v && typeof v === "object" && !Array.isArray(v) ? (v as Record<string, string>) : undefined), {});
  }

  /** @internal What a key holds now; reading one in Setup ties the registration to it. */
  raw(k: Key<unknown>): unknown {
    if (this.inSetup()) k.readInSetup = true;
    return this.values[k.spec.key];
  }

  /** @internal The config after a change: whether a key read in Setup changed. */
  set(values: Record<string, unknown> | undefined): boolean {
    const next = values ?? {};
    const restart = this.keys.some((k) => k.readInSetup && JSON.stringify(this.values[k.spec.key]) !== JSON.stringify(next[k.spec.key]));
    this.values = next;
    return restart;
  }

  /** @internal The keys, as ws is told them. */
  spec(): ConfigKey[] | undefined {
    return this.keys.length ? this.keys.map((k) => k.spec) : undefined;
  }
}

const text = (v: unknown) => (typeof v === "string" ? v : undefined);

function blank(v: unknown): boolean {
  return v === undefined || v === null || v === "" || (Array.isArray(v) && v.length === 0) || (typeof v === "object" && !Array.isArray(v) && Object.keys(v as object).length === 0);
}
