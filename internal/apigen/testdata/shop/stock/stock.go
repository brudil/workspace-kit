// Package stock is what the shop holds.
package stock

// Entry is what's held of something.
type Entry struct {
	ID    string `json:"id"`
	Count int    `json:"count"`
}

// Kind is what sort of thing an item is.
type Kind string

const (
	Book Kind = "book"
	Toy  Kind = "toy"
)

// Audit is exported, and no endpoint sends it: it isn't written.
type Audit struct {
	Entries []Entry
}
