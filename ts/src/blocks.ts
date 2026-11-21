// The blocks a kind's views are drawn from (Ref.card, Ref.pane): data, not
// code, so ws draws them itself.

import type { Block } from "./protocol.gen.ts";
import { BlockBadge, BlockField, BlockMarkdown, BlockSubtitle, BlockTitle, FieldKey } from "./protocol.gen.ts";

/** The field every thing has without its kind saying so: its name (RAM-123). */
export const Key = FieldKey;

/** The line it's known by. */
export const title = (field: string): Block => ({ block: BlockTitle, field });
/** A quiet line under it. */
export const subtitle = (field: string): Block => ({ block: BlockSubtitle, field });
/** A word or two, set apart: where it stands. */
export const badge = (field: string): Block => ({ block: BlockBadge, field });
/** A label and a value: the field's own label, or this one. */
export const field = (field: string, label?: string): Block => (label ? { block: BlockField, field, label } : { block: BlockField, field });
/** Markdown, drawn as ws draws it: all of it, or lines of it. */
export const markdown = (field: string, lines?: number): Block => (lines ? { block: BlockMarkdown, field, lines } : { block: BlockMarkdown, field });
