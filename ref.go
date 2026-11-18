package plugin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/withworkspace/plugin/internal/rpc"
	"github.com/withworkspace/plugin/protocol"
	"github.com/withworkspace/plugin/ui"
)

// A plugin's things have names people and agents write: RAM-123. A plugin
// says how each kind is named, what one is made of, what it's found by and
// how it's drawn (Ref), all of it data, and ws does the rest: it finds the
// names in what's written (a brief, a conversation, a document), links
// them, keeps the things the plugin tells it of, finds those by what they
// say, and draws one under its name and beside the list in Find.

// Ref is a kind of thing the plugin has: how one is named, what it's
// made of (Fields), what it's found by (Search), how it's drawn (Card,
// Pane), and what the plugin tells ws of the ones it has (Mirror, Put,
// Remove).
type Ref struct {
	p    *Plugin
	ref  protocol.Ref
	show func(ctx context.Context, a Asked) (Thing, error)
}

// Asked is one thing ws asks about, by the name it was written as.
type Asked struct {
	// Name is the thing's name: RAM-123.
	Name string
	// Full: everything was asked for (ws show --full). Leave nothing out
	// of what it says; without it, keep to what's worth reading first.
	Full bool
}

// Thing is one of the plugin's things: its name, and a value for each of
// its kind's fields.
type Thing struct {
	// Name is its name, one of the Ref's: RAM-123.
	Name string
	// Changed is when it last changed, if that's known.
	Changed time.Time
	// Fields are its values, by its kind's fields' names (Fields). Each
	// is a string: a time is RFC 3339 (Stamp). One with nothing for its
	// kind's title isn't a thing ws keeps.
	Fields map[string]string
}

// Stamp is a time as a Time field has it.
func Stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// Ref declares a kind of thing the plugin has that words name. kind is
// the plugin's word for it (issue), title what one is called (Jira
// issue). The rest is said after, each a call on what this returns.
//
// A name is a prefix and a number: RAM-123. What a plugin registers
// usually follows its config (which projects, which site), so it's
// declared in Setup:
//
//	p.Setup(func() {
//		issue = p.Ref("issue", "Jira issue").
//			Prefix("RAM-", "RAP-").
//			Address(site.Get() + "/browse/{ref}").
//			Opens("/issue/{ref}").
//			Fields(plugin.Title("title"), plugin.State("status"),
//				plugin.Text("assignee", "Assignee"), plugin.Markdown("body")).
//			Search(plugin.All("title"), plugin.Upto("body", 10000)).
//			Card(ui.Title("title"), ui.Subtitle(ui.Key), ui.Badge("status")).
//			Pane(ui.Title("title"), ui.Subtitle(ui.Key), ui.Badge("status"),
//				ui.Field("assignee"), ui.Markdown("body")).
//			Show(fetchIssue)
//	})
func (p *Plugin) Ref(kind, title string) *Ref {
	p.declaring("Ref")
	for _, was := range p.refs {
		if was.ref.Kind == kind {
			p.declErrs = append(p.declErrs, fmt.Errorf("the ref %q is declared twice", kind))
		}
	}
	r := &Ref{p: p, ref: protocol.Ref{Kind: kind, Title: title}}
	p.refs = append(p.refs, r)
	return r
}

// Prefix is what a name of this kind starts with: letters, digits and
// underscores, starting with a letter and ending in a hyphen (RAM-). A
// name is one of them and a number, as written: ram-12 isn't RAM-12.
func (r *Ref) Prefix(prefixes ...string) *Ref {
	r.ref.Prefixes = append(r.ref.Prefixes, prefixes...)
	return r
}

// Address is where one is outside ws, with {ref} where its name goes:
// https://acme.atlassian.net/browse/{ref}. ws links a name to it in a
// document, and reads one pasted as the thing it names.
func (r *Ref) Address(template string) *Ref {
	r.ref.Address = template
	return r
}

// Opens is where one opens in the plugin's app, with {ref} where its name
// goes: /issue/{ref}. Without it, a name goes to its address.
func (r *Ref) Opens(path string) *Ref {
	r.ref.Path = path
	return r
}

// Icon is the kind's icon: one of ws's icon names.
func (r *Ref) Icon(name string) *Ref {
	r.ref.Icon = name
	return r
}

// A field of a kind's things, as Fields takes them.
type FieldSpec = protocol.Field

func field(name, kind string, label []string) FieldSpec {
	return FieldSpec{Name: name, Type: kind, Label: strings.Join(label, " ")}
}

// Title is the field a thing's called by: a line. A kind has one, and a
// thing with nothing for it isn't one ws keeps.
func Title(name string) FieldSpec { return field(name, protocol.FieldTitle, nil) }

// State is the field that says where a thing stands, in the plugin's own
// words (In Progress): a kind has one at most. label is what it's called
// where it's drawn with one.
func State(name string, label ...string) FieldSpec { return field(name, protocol.FieldState, label) }

// Text is a field of words: a person's name, a priority, a number.
func Text(name string, label ...string) FieldSpec { return field(name, protocol.FieldText, label) }

// Time is a field that's a moment: its value is Stamp of it.
func Time(name string, label ...string) FieldSpec { return field(name, protocol.FieldTime, label) }

// Markdown is a field that says something at length: a description, what's
// been said on the thing.
func Markdown(name string, label ...string) FieldSpec {
	return field(name, protocol.FieldMarkdown, label)
}

// Fields are what one of this kind's things is made of: what Mirror, Put
// and Show give a value for. Every thing has ui.Key besides, its name.
func (r *Ref) Fields(fields ...FieldSpec) *Ref {
	r.ref.Fields = append(r.ref.Fields, fields...)
	return r
}

// Searched is a field a thing is found by, as Search takes them.
type Searched = protocol.Searched

// All is the whole of a field, as far as ws keeps it.
func All(field string) Searched { return Searched{Field: field} }

// Upto is the start of a field: its first max bytes.
func Upto(field string, max int) Searched { return Searched{Field: field, Max: max} }

// Search is what one of this kind's things is found by, in order: ws
// makes the words it looks through from these, and makes them again when
// they change, without the plugin telling it anything twice. Without it,
// a thing's found by every field it has.
func (r *Ref) Search(by ...Searched) *Ref {
	r.ref.Search = append(r.ref.Search, by...)
	return r
}

// Card is how one is drawn under its name, where it's written (on hover):
// a few lines.
func (r *Ref) Card(blocks ...ui.Block) *Ref { return r.view(protocol.ViewCard, blocks) }

// Pane is how one is drawn beside the list in Find, for the row the
// cursor's on: there's room for what it says.
func (r *Ref) Pane(blocks ...ui.Block) *Ref { return r.view(protocol.ViewPane, blocks) }

func (r *Ref) view(where string, blocks []ui.Block) *Ref {
	if r.ref.Views == nil {
		r.ref.Views = map[string][]protocol.Block{}
	}
	wire := make([]protocol.Block, 0, len(blocks))
	for _, b := range blocks {
		wire = append(wire, b.Wire())
	}
	r.ref.Views[where] = wire
	return r
}

// Show is what answers for one thing, now: fn is given a name ws found,
// and says what the plugin has by it, or ErrNotFound for nothing. ws asks
// for a thing it hasn't been told of (one off the board), and when what
// it's been told isn't enough: ws show RAM-123, which wants it as it is
// now, and in full. A kind with no Show is shown as ws was last told.
func (r *Ref) Show(fn func(ctx context.Context, a Asked) (Thing, error)) *Ref {
	r.show = fn
	return r
}

// refsDeclared are the refs as ws is told them, and what's wrong with
// any of them.
func (p *Plugin) refsDeclared() ([]protocol.Ref, []error) {
	var out []protocol.Ref
	var errs []error
	for _, r := range p.refs {
		if problem := r.ref.Problem(); problem != "" {
			errs = append(errs, fmt.Errorf("the ref %q: %s", r.ref.Kind, problem))
			continue
		}
		out = append(out, r.ref)
	}
	return out, errs
}

// showRef answers ws asking what one of the plugin's things is.
func (p *Plugin) showRef(ctx context.Context, in protocol.RefShowParams) (any, error) {
	var ref *Ref
	for _, r := range p.refs {
		if r.ref.Kind == in.Kind {
			ref = r
		}
	}
	if ref == nil {
		return nil, &rpc.Error{Code: protocol.CodeInvalidParams, Message: fmt.Sprintf("this plugin has no ref called %q", in.Kind)}
	}
	if ref.show == nil {
		// It says no more than it's told ws already.
		return nil, rpc.MethodNotFound(protocol.MethodRefShow)
	}
	if p.broken != nil {
		return nil, p.broken
	}
	ctx, cancel := context.WithTimeout(ctx, p.CommandTimeout)
	defer cancel()
	var thing Thing
	err := p.recovering("showing "+in.Ref, func() (err error) {
		thing, err = ref.show(ctx, Asked{Name: in.Ref, Full: in.Full})
		return err
	})
	switch {
	case errors.Is(err, ErrNotFound):
		return nil, &rpc.Error{Code: protocol.CodeNotFound, Message: fmt.Sprintf("there's no %s %s", in.Kind, in.Ref)}
	case err != nil:
		return nil, err
	}
	fields := thing.Fields
	if fields == nil {
		fields = map[string]string{}
	}
	return protocol.RefShowResult{Changed: thing.Changed, Fields: fields}, nil
}

// Refused is ws not keeping some of what it was told: each thing, and
// why. The others were kept.
type Refused struct {
	Things []protocol.ThingRefused
}

func (e *Refused) Error() string {
	said := make([]string, 0, len(e.Things))
	for _, t := range e.Things {
		said = append(said, t.Ref+": "+t.Why)
	}
	if len(said) > 3 {
		said = append(said[:3], fmt.Sprintf("and %d more", len(e.Things)-3))
	}
	return "ws didn't keep " + strings.Join(said, "; ")
}

// Mirror tells ws every thing of this kind the plugin has: each is kept
// as it's given, and what ws had of the kind that isn't among them is
// forgotten. It's what a sync calls when it's done: ws finds the plugin's
// things by what they say, and draws them, without asking the plugin.
// What ws wouldn't keep is a *Refused, after the rest is done.
//
//	p.Sync("issues", 5*time.Minute, func(ctx context.Context) error {
//		…
//		return issue.Mirror(ctx, things)
//	})
func (r *Ref) Mirror(ctx context.Context, things []Thing) error {
	var had protocol.ThingListResult
	if err := r.p.ask(ctx, protocol.MethodThingList, protocol.ThingListParams{Kind: r.ref.Kind}, &had); err != nil {
		return err
	}
	have := map[string]bool{}
	for _, t := range things {
		have[t.Name] = true
	}
	refused := r.Put(ctx, things...)
	var no *Refused
	if refused != nil && !errors.As(refused, &no) {
		return refused
	}
	gone := slices.DeleteFunc(had.Refs, func(name string) bool { return have[name] })
	if err := r.Remove(ctx, gone...); err != nil {
		return err
	}
	return refused
}

// Put tells ws of things of this kind: each is kept, in place of what ws
// had by that name. What ws wouldn't keep is a *Refused.
func (r *Ref) Put(ctx context.Context, things ...Thing) error {
	var refused []protocol.ThingRefused
	for batch := range slices.Chunk(things, protocol.ThingsAtOnce) {
		in := protocol.ThingPutParams{Things: make([]protocol.Thing, 0, len(batch))}
		for _, t := range batch {
			fields := t.Fields
			if fields == nil {
				fields = map[string]string{}
			}
			in.Things = append(in.Things, protocol.Thing{Kind: r.ref.Kind, Ref: t.Name, Changed: t.Changed, Fields: fields})
		}
		var out protocol.ThingPutResult
		if err := r.p.ask(ctx, protocol.MethodThingPut, in, &out); err != nil {
			return err
		}
		refused = append(refused, out.Refused...)
	}
	if len(refused) > 0 {
		return &Refused{Things: refused}
	}
	return nil
}

// Remove has ws forget things of this kind, by name. One it doesn't have
// is nothing to forget.
func (r *Ref) Remove(ctx context.Context, names ...string) error {
	if len(names) == 0 {
		return nil
	}
	return r.p.ask(ctx, protocol.MethodThingRemove, protocol.ThingRemoveParams{Kind: r.ref.Kind, Refs: names}, nil)
}
