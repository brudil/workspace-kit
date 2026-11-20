// A plugin for apigen's test: each way an endpoint is declared, and each
// kind of type one takes or answers.
package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"example.com/shop/people"
	"example.com/shop/stock"
	"github.com/withworkspace/plugin"
)

// Item is something the shop sells.
type Item struct {
	stock.Entry
	Name string `json:"name"`
	// Kind is what sort of thing it is.
	Kind  stock.Kind `json:"kind"`
	State State      `json:"state"`
	Aged  Aged       `json:"aged"`
	Tags  []string   `json:"tags"`
	Notes []Note     `json:"notes,omitempty"`
	// Parent is what it's part of, if anything.
	Parent *Ref              `json:"parent"`
	Added  time.Time         `json:"added"`
	Price  Money             `json:"price"`
	Code   Code              `json:"code"`
	Extra  json.RawMessage   `json:"extra,omitempty"`
	Attrs  map[string]string `json:"attrs"`
	Hidden string            `json:"-"`
	secret string
}

// State is whether an item can be bought.
type State string

const (
	InStock State = "in stock"
	SoldOut State = "sold out"
)

// Aged is how long an item's been held. Its constants aren't exported,
// and it's the union of them all the same.
type Aged string

const (
	agedNew Aged = "new"
	agedOld Aged = "old"
	// Vintage is exported, and is one of them with the rest.
	Vintage Aged = "vintage"
)

// Note is something said of an item.
type Note struct {
	By   people.Person `json:"by"`
	Text string        `json:"text"`
}

// Ref is another item, by its id.
type Ref struct {
	ID string `json:"id"`
}

// Money writes its own JSON, so the app is told nothing of it.
type Money struct{ pence int }

func (m Money) MarshalJSON() ([]byte, error) { return json.Marshal(m.pence) }

// Code is written as text.
type Code struct{ n int }

func (c Code) MarshalText() ([]byte, error) { return []byte("C"), nil }

// Page is some of a list, and where the rest starts.
type Page[T any] struct {
	Items []T    `json:"items"`
	Next  string `json:"next,omitempty"`
}

// Shelf is where things are kept.
type Shelf struct {
	Name    string        `json:"name"`
	Entries []stock.Entry `json:"entries"`
}

// Find is which items are asked for: a query.
type Find struct {
	Kind stock.Kind `json:"kind,omitempty"`
	// Text is words to look for.
	Text  string    `json:"q,omitempty"`
	Limit int       `json:"limit"`
	Tags  []string  `json:"tag,omitempty"`
	Since time.Time `json:"since,omitempty"`
	Mine  *bool     `json:"mine,omitempty"`
	At    Aisle     `json:"at,omitempty"`
	Paging
	Hidden string `json:"-"`
}

// Paging is part of a query, as its own fields.
type Paging struct {
	Page int `json:"page,omitempty"`
}

// Aisle reads itself from text, and doesn't write itself as it.
type Aisle struct{ n int }

func (a *Aisle) UnmarshalText(text []byte) error { return nil }

// NewItem is what's sent to add an item, or to put one in place of another.
type NewItem struct {
	Name string     `json:"name"`
	Kind stock.Kind `json:"kind"`
}

// Unsent is exported, and no endpoint sends it: it isn't written.
type Unsent struct {
	Done chan struct{}
}

// Limit isn't a type: it isn't written.
const Limit = 10

const versionPath = "/api/version"

var errStub = errors.New("stub")

type shop struct{ p *plugin.Plugin }

func main() {
	p := plugin.New(nil)
	(&shop{p}).routes()
	people.Routes(p)
	p.Run()
}

func (s *shop) routes() {
	plugin.Get(s.p, "/api/items", func(r *http.Request) (Page[Item], error) { return Page[Item]{}, errStub })
	plugin.Post(s.p, "/api/items", func(r *http.Request, in NewItem) (Item, error) { return Item{}, errStub })
	plugin.Get(s.p, "/api/items/{id}", func(r *http.Request) (*Item, error) { return nil, errStub })
	plugin.Put(s.p, "/api/items/{id}", func(r *http.Request, in NewItem) (Item, error) { return Item{}, errStub })
	plugin.Patch(s.p, "/api/items/{id}", func(r *http.Request, in map[string]string) (Item, error) { return Item{}, errStub })
	plugin.Delete(s.p, "/api/items/{id}", func(r *http.Request) (struct{}, error) { return struct{}{}, errStub })
	plugin.Post(s.p, "/api/sync", func(r *http.Request, _ struct{}) (struct{}, error) { return struct{}{}, errStub })
	plugin.Get(s.p, "/api/find", plugin.Query(func(r *http.Request, q Find) ([]Item, error) { return nil, errStub }))
	plugin.Delete(s.p, "/api/found", plugin.Query[Paging, struct{}](func(r *http.Request, q Paging) (struct{}, error) { return struct{}{}, errStub }))
	plugin.Get(s.p, "/api/files/{path...}", func(r *http.Request) (string, error) { return "", errStub })
	plugin.Get[map[string]int](s.p, "/api/counts", counts)
	plugin.Get(s.p, versionPath, func(r *http.Request) (string, error) { return "", errStub })
	plugin.Get(s.p, "/api/"+"shelves", func(r *http.Request) ([]*Shelf, error) { return nil, errStub })

	// Not an endpoint: a handler, which the app is told nothing of.
	s.p.HandleFunc("GET /api/export", func(w http.ResponseWriter, r *http.Request) {})
}

func counts(r *http.Request) (map[string]int, error) { return nil, errStub }
