// Package ui is how a plugin says what one of its things looks like where
// ws draws it: under its name, beside the list in Find. A view is blocks,
// in order, each showing one of the thing's fields. It's said in Go and
// sent to ws as data, which draws it in its own web UI, in its own
// styles: the plugin writes no markup, and nothing of its runs there.
//
//	issue.Card(ui.Title("title"), ui.Subtitle(ui.Key), ui.Badge("status"))
//	issue.Pane(ui.Title("title"), ui.Subtitle(ui.Key), ui.Badge("status"),
//		ui.Field("assignee"), ui.Markdown("body"))
//
// A plugin's own app, for what's more than looking (a board, a form), is
// another thing: a page ws frames (plugin.UI).
package ui

import "github.com/withworkspace/plugin/protocol"

// Key is the field every thing has: its name (RAM-123).
const Key = protocol.FieldKey

// Block is one piece of a view: what it is, and the field it shows. One
// whose field a thing has nothing for isn't drawn.
type Block struct{ b protocol.Block }

// Title is the line a thing's known by.
func Title(field string) Block {
	return Block{protocol.Block{Block: protocol.BlockTitle, Field: field}}
}

// Subtitle is a quiet line under the title.
func Subtitle(field string) Block {
	return Block{protocol.Block{Block: protocol.BlockSubtitle, Field: field}}
}

// Badge is a word or two set apart: where the thing stands. After a
// title or a subtitle, it's drawn on their line.
func Badge(field string) Block {
	return Block{protocol.Block{Block: protocol.BlockBadge, Field: field}}
}

// Field is a label and a value: Assignee: Dana. Its label is the field's
// own, as the kind declared it, unless Label says another.
func Field(field string) Block {
	return Block{protocol.Block{Block: protocol.BlockField, Field: field}}
}

// Markdown is a field drawn as markdown, as ws draws it everywhere: all
// of it, unless Lines says how much.
func Markdown(field string) Block {
	return Block{protocol.Block{Block: protocol.BlockMarkdown, Field: field}}
}

// Label is what a Field says before its value, in place of the field's
// own label.
func (b Block) Label(text string) Block {
	b.b.Label = text
	return b
}

// Lines is how many lines of a Markdown block are drawn: a card has room
// for a few.
func (b Block) Lines(n int) Block {
	b.b.Lines = n
	return b
}

// Wire is the block as ws is told it.
func (b Block) Wire() protocol.Block { return b.b }
