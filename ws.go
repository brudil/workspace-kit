package plugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// A plugin reaches ws as anything else does: by running its commands. ws
// says where its program is when the plugin starts.

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
