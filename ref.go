package plugin

import (
	"context"
	"errors"
	"fmt"

	"github.com/withworkspace/plugin/internal/rpc"
	"github.com/withworkspace/plugin/protocol"
)

// A plugin's things have names people and agents write: RAM-123. A plugin
// says how each kind is named (Ref), and ws does the rest: it finds the
// names in what's written (a brief, a conversation, a document), links
// them, and asks the plugin what one is when it's shown (ws show RAM-123,
// the card under a name in the web UI).

// Ref is a kind of thing the plugin has, and how one is named.
type Ref struct {
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
	r := &Ref{ref: protocol.Ref{Kind: kind, Title: title}, show: show}
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
