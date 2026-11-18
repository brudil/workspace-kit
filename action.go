package plugin

import (
	"context"
	"errors"
	"fmt"

	"github.com/withworkspace/plugin/internal/rpc"
	"github.com/withworkspace/plugin/protocol"
)

// A plugin's actions are what it can do when it's asked from ws's Find:
// its own (Plugin.Action: sync now, a new issue), offered among what
// there is to do, and what can be done to one of its things (Ref.Action:
// move it, assign it), offered for the thing the cursor's on. Either may
// ask for something first, a pick from a list or a line typed, which Find
// asks and sends back.

// Action is something the plugin can do, as Find offers it.
type Action struct {
	act protocol.Action
	fn  func(*Act) error
}

// Action declares something the plugin can do, and is what does it: Find
// offers it by its title, and fn is run when it's chosen. What fn says
// (Act.Say) is told to whoever chose it, and where it opens (Act.Open) is
// gone to.
//
// An action that needs something first asks for it: Act.Pick, Act.Ask.
// Asking doesn't wait. fn is run again, from the start, once it's
// answered, so everything fn does before its last ask is done again:
// ask first, and do the work after.
//
//	p.Action("new", "New issue…", func(a *plugin.Act) error {
//		title, err := a.Ask("What's the issue?", "A title")
//		if err != nil {
//			return err
//		}
//		key, err := create(a.Context(), title)
//		if err != nil {
//			return err
//		}
//		a.Open("/issue/" + key)
//		return a.Say("Created " + key)
//	})
func (p *Plugin) Action(id, title string, fn func(a *Act) error) *Action {
	p.declaring("Action")
	a := &Action{act: protocol.Action{ID: id, Title: title}, fn: fn}
	p.actions = append(p.actions, a)
	return a
}

// Icon is the action's icon in Find: one of ws's icon names.
func (a *Action) Icon(name string) *Action {
	a.act.Icon = name
	return a
}

// Hint says more of the action, after its title.
func (a *Action) Hint(text string) *Action {
	a.act.Hint = text
	return a
}

// Act is one run of an action.
type Act struct {
	// Name is the thing it's done to (RAM-123): "" for one of the
	// plugin's own.
	Name string

	ctx     context.Context
	answers []string
	asked   int
	out     protocol.ActionRunResult
}

// errAsking is what Pick and Ask return when what they ask hasn't been
// answered yet: the action returns it, and Find asks.
var errAsking = errors.New("the action is asking")

// Context ends when whoever chose the action gives up on it.
func (a *Act) Context() context.Context { return a.ctx }

// Choice is one thing to pick: what's answered (Value), what it's called
// (Label), and a word more (Hint).
type Choice = protocol.Option

// Choices are choices each answered by what it's called.
func Choices(labels ...string) []Choice {
	out := make([]Choice, 0, len(labels))
	for _, l := range labels {
		out = append(out, Choice{Value: l, Label: l})
	}
	return out
}

// Pick asks for one of choices, by title, and is its value. Until it's
// answered, its error is one the action returns as it is.
func (a *Act) Pick(title string, choices ...Choice) (string, error) {
	return a.ask(protocol.Ask{Title: title, Type: protocol.AskPick, Options: choices})
}

// Ask asks for a line of text, by title. Until it's answered, its error
// is one the action returns as it is.
func (a *Act) Ask(title, placeholder string) (string, error) {
	return a.ask(protocol.Ask{Title: title, Type: protocol.AskText, Placeholder: placeholder})
}

func (a *Act) ask(q protocol.Ask) (string, error) {
	if a.asked < len(a.answers) {
		a.asked++
		return a.answers[a.asked-1], nil
	}
	a.out.Ask = &q
	return "", errAsking
}

// Say is a line for whoever chose the action, once it's done.
func (a *Act) Say(text string) error {
	a.out.Say = text
	return nil
}

// Open is where to go once it's done: a path in the plugin's app.
func (a *Act) Open(path string) { a.out.Open = path }

// actionsDeclared are the plugin's own actions as ws is told them, and
// what's wrong with them.
func (p *Plugin) actionsDeclared() ([]protocol.Action, error) {
	var out []protocol.Action
	for _, a := range p.actions {
		if a.fn == nil {
			return nil, fmt.Errorf("the action %q has nothing that does it", a.act.ID)
		}
		out = append(out, a.act)
	}
	if problem := protocol.ActionsProblem(out); problem != "" {
		return nil, errors.New(problem)
	}
	return out, nil
}

// runAction answers ws running an action: the plugin's own, or one of a
// kind's on one of its things.
func (p *Plugin) runAction(ctx context.Context, in protocol.ActionRunParams) (any, error) {
	var fn func(*Act) error
	if in.Kind == "" {
		for _, a := range p.actions {
			if a.act.ID == in.ID {
				fn = a.fn
			}
		}
	} else {
		for _, r := range p.refs {
			if r.ref.Kind == in.Kind {
				fn = r.acts[in.ID]
			}
		}
	}
	if fn == nil {
		return nil, &rpc.Error{Code: protocol.CodeInvalidParams, Message: fmt.Sprintf("this plugin has no action called %q", in.ID)}
	}
	if p.broken != nil {
		return nil, p.broken
	}
	ctx, cancel := context.WithTimeout(ctx, p.CommandTimeout)
	defer cancel()
	act := &Act{Name: in.Ref, ctx: ctx, answers: in.Answers}
	err := p.recovering("the action "+in.ID, func() error { return fn(act) })
	switch {
	case errors.Is(err, errAsking) && act.out.Ask != nil:
		return protocol.ActionRunResult{Ask: act.out.Ask}, nil
	case err != nil:
		return nil, err
	}
	act.out.Ask = nil
	return act.out, nil
}
