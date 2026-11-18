package plugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/withworkspace/plugin/internal/rpc"
	"github.com/withworkspace/plugin/protocol"
)

// A plugin asks ws what it knows over the connection they share, and gets
// it in the protocol's types: p.Missions, p.Mission, p.Repos, p.Show, and
// has it start a conversation (p.StartConversation). What ws
// doesn't answer that way yet, a plugin can run a ws command for (p.WS):
// ws says where its program is when the plugin starts.

// ErrNotFound is the answer for something asked for by name that isn't
// there: ws's for a mission with no such slug, and what a plugin's Ref
// returns for a name it has nothing by.
var ErrNotFound = errors.New("not found")

// ErrUnsupported is what asking ws gives when the ws the plugin is
// running in is from before it could answer that.
var ErrUnsupported = errors.New("this ws can't answer that: update ws")

// Missions are the workspace's missions in play, and with archived, the
// ones archived too: in play first.
func (p *Plugin) Missions(ctx context.Context, archived bool) ([]protocol.Mission, error) {
	var out protocol.MissionListResult
	if err := p.ask(ctx, protocol.MethodMissionList, protocol.MissionListParams{Archived: archived}, &out); err != nil {
		return nil, err
	}
	return out.Missions, nil
}

// Mission is a mission, by its slug, with its brief and plan. One that
// isn't there is ErrNotFound.
func (p *Plugin) Mission(ctx context.Context, slug string) (protocol.MissionDetail, error) {
	var out protocol.MissionDetail
	err := p.ask(ctx, protocol.MethodMissionShow, protocol.MissionShowParams{Slug: slug}, &out)
	return out, err
}

// Show is one of the workspace's things as ws show shows it: what it is,
// what it links to, and what points at it from elsewhere in the
// workspace. ref names it as ws show does: one of a plugin's things
// (RAM-123), a pull request (api#12), a document (records:docs/plan.md),
// one of the workspace's conversations, pages or artifacts (w4, p2, a3).
// Nothing by that name is ErrNotFound.
//
//	shown, err := p.Show(ctx, "RAM-123")
//	for _, s := range shown.Said { … } // the missions about it, what names it
//
// What it links to and what points at it are each asked for in turn, so a
// plugin goes from one thing to the next: ShowIn for one of a mission's
// own.
func (p *Plugin) Show(ctx context.Context, ref string) (protocol.Shown, error) {
	return p.ShowIn(ctx, "", ref)
}

// ShowIn is one of a mission's own things, by its ref there (t3, d1, s2,
// c1, r1.c2): what Show does for the workspace's. With no ref it's the
// mission itself, which Show is too, given its slug.
func (p *Plugin) ShowIn(ctx context.Context, mission, ref string) (protocol.Shown, error) {
	var out protocol.Shown
	err := p.ask(ctx, protocol.MethodShow, protocol.ShowParams{Ref: ref, Mission: mission}, &out)
	return out, err
}

// StartConversation starts one of the workspace's conversations for the
// person using the plugin, with message as what they say first, and has
// its agent answer. It's how a plugin's app hands something on to ws to
// be talked over, and from there made a mission of: the agent reads what
// the message names (RAM-123) with ws show, and proposes what the
// conversation comes to, for the person to accept. What's returned is the
// conversation and its page, for the app to open.
//
//	c, err := p.StartConversation(ctx, "Let's work out a mission for RAM-123.")
//
// It sends a message as the person, so it's for something they've asked
// for (a button they pressed), not for the plugin's own account: ws
// records that the plugin started it.
func (p *Plugin) StartConversation(ctx context.Context, message string) (protocol.ConversationStarted, error) {
	var out protocol.ConversationStarted
	err := p.ask(ctx, protocol.MethodConversationStart, protocol.ConversationStartParams{Message: message}, &out)
	return out, err
}

// Repos are the workspace's repos, in ws's order, each with its capsules.
func (p *Plugin) Repos(ctx context.Context) ([]protocol.Repo, error) {
	var out protocol.RepoListResult
	if err := p.ask(ctx, protocol.MethodRepoList, struct{}{}, &out); err != nil {
		return nil, err
	}
	return out.Repos, nil
}

// ask sends ws a request, and says what its answer's errors are.
func (p *Plugin) ask(ctx context.Context, method string, params, result any) error {
	p.mu.Lock()
	conn := p.conn
	p.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("%s: the plugin isn't connected to ws: it's asked once Run has started", method)
	}
	err := conn.Call(ctx, method, params, result)
	var e *rpc.Error
	if errors.As(err, &e) {
		switch e.Code {
		case protocol.CodeMethodNotFound:
			return fmt.Errorf("ws %s can't answer %s: %w", p.where().WS.Version, method, ErrUnsupported)
		case protocol.CodeNotFound:
			return fmt.Errorf("%s: %w", e.Message, ErrNotFound)
		}
	}
	return err
}

// ErrNoWS is what running a ws command gives when ws didn't say where its
// program is: a ws from before it did, or a test's stand-in without one.
var ErrNoWS = errors.New("ws didn't say where its program is, so its commands can't be run")

// WSCommand is a ws command, ready to run in the workspace: ws followed
// by args. Its output is the caller's to connect. nil if ws didn't say
// where its program is.
//
//	cmd := p.WSCommand(ctx, "plugin", "list", "-f", "json")
func (p *Plugin) WSCommand(ctx context.Context, args ...string) *exec.Cmd {
	info := p.where()
	if info.WS.Executable == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, info.WS.Executable, args...)
	cmd.Dir = info.Workspace.Root
	// ws works out which workspace it's in from where it's run: here, not
	// wherever whoever started the daemon was standing.
	cmd.Env = append(os.Environ(), "WS_ORIGIN_PWD="+info.Workspace.Root)
	return cmd
}

// WS runs a ws command in the workspace and returns what it printed. An
// error says what it printed to stderr, and what it printed to stdout is
// returned even so: a command that fails may still have said something.
//
//	out, err := p.WS(ctx, "show", "m12", "-f", "llm")
func (p *Plugin) WS(ctx context.Context, args ...string) ([]byte, error) {
	cmd := p.WSCommand(ctx, args...)
	if cmd == nil {
		return nil, ErrNoWS
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		said := strings.TrimSpace(stderr.String())
		if said == "" {
			said = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("ws %s: %s", strings.Join(args, " "), said)
	}
	return stdout.Bytes(), nil
}
