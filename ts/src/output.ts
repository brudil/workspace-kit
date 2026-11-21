// What a command prints: a line, one thing, or rows. For a person it's
// text sized to their terminal; for -f json, JSON; for -f llm, markdown,
// for an agent. As the Go kit's output.go prints them.

/** The formats a command prints in. */
export const FormatTable = "table";
export const FormatJSON = "json";
export const FormatLLM = "llm";
export type Format = typeof FormatTable | typeof FormatJSON | typeof FormatLLM;

/** A row's fields as columns: their names, capitalised, and their values as text. */
export function tabulate(rows: readonly unknown[]): { cols: string[]; cells: string[][] } {
  let keys: string[] | undefined;
  const cells: string[][] = [];
  for (const row of rows) {
    if (row && typeof row === "object" && !Array.isArray(row)) {
      keys ??= Object.keys(row);
      cells.push(keys.map((k) => cell((row as Record<string, unknown>)[k])));
    } else {
      keys ??= [];
      cells.push([cell(row)]);
    }
  }
  const cols = keys?.length ? keys.map(heading) : ["Value"];
  return { cols, cells };
}

const heading = (key: string) => key.charAt(0).toUpperCase() + key.slice(1);

function cell(v: unknown): string {
  if (v === undefined || v === null) return "";
  if (v instanceof Date) return isNaN(v.getTime()) ? "" : local(v);
  if (typeof v === "boolean") return v ? "yes" : "no";
  if (Array.isArray(v) && v.every((s) => typeof s === "string")) return v.join(", ");
  if (typeof v === "object") return JSON.stringify(v);
  return String(v).replaceAll("\n", " ");
}

function local(d: Date): string {
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`;
}

const runes = (s: string) => [...s].length;

function clip(s: string, width: number): string {
  const r = [...s];
  return r.length <= width ? s : r.slice(0, Math.max(0, width - 1)).join("") + "…";
}

/** Rows as a table for a person: a heading in capitals, columns sized to width (0: as wide as they are). */
export function textTable(cols: string[], cells: string[][], width: number, color: boolean): string {
  const widths = cols.map(runes);
  for (const row of cells) row.forEach((c, i) => (widths[i] = Math.max(widths[i] ?? 0, runes(c))));
  const gap = 2;
  const least = 8;
  if (width > 0) {
    for (;;) {
      let total = gap * (widths.length - 1);
      let widest = 0;
      widths.forEach((w, i) => {
        total += w;
        if (w > widths[widest]!) widest = i;
      });
      if (total <= width || widths[widest]! <= least) break;
      widths[widest] = Math.max(least, widths[widest]! - (total - width));
    }
  }
  const line = (row: string[], bold: boolean) =>
    row
      .map((c, i) => {
        let out = clip(c, widths[i]!);
        if (i < row.length - 1) out += " ".repeat(widths[i]! - runes(out) + gap);
        return bold ? `\x1b[1m${out}\x1b[0m` : out;
      })
      .join("") + "\n";
  return line(cols.map((c) => c.toUpperCase()), color) + cells.map((r) => line(r, false)).join("");
}

/** Rows as a markdown table, for an agent. */
export function markdownTable(cols: string[], cells: string[][]): string {
  const esc = (s: string) => s.replaceAll("|", "\\|");
  const row = (r: string[]) => "|" + r.map((c) => ` ${esc(c)} |`).join("") + "\n";
  return row(cols) + "|" + cols.map(() => " --- |").join("") + "\n" + cells.map(row).join("");
}
