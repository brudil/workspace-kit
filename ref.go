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
)

// A plugin's things have names people and agents write: RAM-123. A plugin
// says how each kind is named (Ref), and ws does the rest: it finds the
// names in what's written (a brief, a conversation, a document), links
// them, and asks the plugin what one is when it's shown (ws show RAM-123,
// the card under a name in the web UI).

// Ref is a kind of thing the plugin has, and how one is named. What ws
// has of the kind without asking is told through it: Mirror, Put, Remove.
type Ref struct {
	p    *Plugin
	ref  protocol.Ref
	show func(ctx context.Context, a Asked) (Shown, error)
}

// Asked is one thing ws asks about, by the name it was written as.
type Asked struct {
	// Name is the thing's name: RAM-123.
	Name string
	// Full: everything was asked for (ws show --full). Leave nothing out
	// of the markdown; without it, keep to what's worth reading first.
	Full bool
}

// Shown is a thing as ws shows it: in a card under its name, and as ws
// show prints it.
type Shown struct {
	Title string
	// State is where it stands, in the plugin's words: In Progress.
	State string
	// Fields are a few things worth a line each: who has it, how pressing
	// it is.
	Fields []Field
	// Markdown is what it says, for ws show: an agent reads it as
	// information, with where it came from said before it.
	Markdown string
}

// Field is one thing said of a thing: Field{"Assignee", "Dana"}.
type Field struct{ Label, Value string }

// Ref declares a kind of thing the plugin has that words name, and is
// what answers for one: show is given a name ws found, and says what it
// is, or ErrNotFound when the plugin has nothing by it. kind is the
// plugin's word for it (issue), title what one is called (Jira issue).
// How one is named is said after: Prefix, Address, Opens, Icon.
//
// A name is a prefix and a number: RAM-123. What a plugin registers
// usually follows its config (which projects, which site), so it's
// declared in Setup:
//
//	p.Setup(func() {
//		p.Ref("issue", "Jira issue", showIssue).
//			Prefix("RAM-", "RAP-").
//			Address(site.Get() + "/browse/{ref}").
//			Opens("/issue/{ref}").
//			Icon("ticket")
//	})
func (p *Plugin) Ref(kind, title string, show func(ctx context.Context, a Asked) (Shown, error)) *Ref {
	p.declaring("Ref")
	for _, was := range p.refs {
		if was.ref.Kind == kind {
			p.declErrs = append(p.declErrs, fmt.Errorf("the ref %q is declared twice", kind))
		}
	}
	if show == nil {
		p.declErrs = append(p.declErrs, fmt.Errorf("the ref %q has nothing to show one: ws asks what a name is", kind))
	}
	r := &Ref{p: p, ref: protocol.Ref{Kind: kind, Title: title}, show: show}
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
	if ref == nil || ref.show == nil {
		return nil, &rpc.Error{Code: protocol.CodeInvalidParams, Message: fmt.Sprintf("this plugin has no ref called %q", in.Kind)}
	}
	if p.broken != nil {
		return nil, p.broken
	}
	ctx, cancel := context.WithTimeout(ctx, p.CommandTimeout)
	defer cancel()
	var shown Shown
	err := p.recovering("showing "+in.Ref, func() (err error) {
		shown, err = ref.show(ctx, Asked{Name: in.Ref, Full: in.Full})
		return err
	})
	switch {
	case errors.Is(err, ErrNotFound):
		return nil, &rpc.Error{Code: protocol.CodeNotFound, Message: fmt.Sprintf("there's no %s %s", in.Kind, in.Ref)}
	case err != nil:
		return nil, err
	}
	out := protocol.RefShowResult{Title: shown.Title, State: shown.State, Markdown: shown.Markdown}
	for _, f := range shown.Fields {
		out.Fields = append(out.Fields, protocol.RefField{Label: f.Label, Value: f.Value})
	}
	return out, nil
}

// Thing is one of the plugin's things, as ws keeps it: so it's found by
// what it says (ws find, Find in the web UI) and named without the plugin
// being asked. What it is in full is still the Ref's show to say.
type Thing struct {
	// Name is its name, one of the Ref's: RAM-123.
	Name string
	// Title is what it's called. ws keeps nothing with no title.
	Title string
	// State is where it stands, in the plugin's words: In Progress.
	State string
	// Changed is when it last changed, if that's known.
	Changed time.Time
	// Text is what it says, to be found by: its description, what's been
	// said on it. ws keeps the start of a long one.
	Text string
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
// things by what they say, and names them, without asking the plugin.
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
			in.Things = append(in.Things, protocol.Thing{Kind: r.ref.Kind, Ref: t.Name, Title: t.Title, State: t.State, Changed: t.Changed, Text: t.Text})
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
