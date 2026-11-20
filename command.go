package plugin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/withworkspace/plugin/protocol"
)

// Command is one thing the plugin's command does: ws <plugin> <name>.
type Command struct {
	name  string
	short string
	args  string
	fn    func(*Call) error
	flags []flag
	// timeout is how long it may run, where that isn't the plugin's
	// CommandTimeout.
	timeout *time.Duration
	// conversations is a command a conversation's agent may run.
	conversations bool
	// label is what it's doing, as a conversation shows it.
	label string
}

// flag is a flag a command takes: on or off, or (takesValue) with a value.
type flag struct {
	name       string
	usage      string
	takesValue bool
}

// Command declares a command and is its handler: ws <plugin> <name> runs
// fn. What fn prints through the Call reaches whoever ran it, a person or
// an agent, in the format they asked for (-f table, json or llm).
//
//	p.Command("mine", "Issues assigned to you", func(c *plugin.Call) error {
//		return c.List(issues)
//	}).Flag("all", "Include closed issues")
func (p *Plugin) Command(name, short string, fn func(c *Call) error) *Command {
	p.declaring("Command")
	for _, was := range p.commands {
		if was.name == name {
			p.declErrs = append(p.declErrs, fmt.Errorf("command %q is declared twice", name))
		}
	}
	c := &Command{name: name, short: short, fn: fn}
	p.commands = append(p.commands, c)
	return c
}

// Flag declares a flag that's on or off: --name. Call.Flag reads it.
func (c *Command) Flag(name, usage string) *Command {
	c.flags = append(c.flags, flag{name: name, usage: usage})
	return c
}

// Option declares a flag with a value: --name value. Call.Option reads it.
func (c *Command) Option(name, usage string) *Command {
	c.flags = append(c.flags, flag{name: name, usage: usage, takesValue: true})
	return c
}

// Args says what the command takes after its flags, for its help: "KEY",
// "[QUERY]".
func (c *Command) Args(usage string) *Command {
	c.args = usage
	return c
}

// Conversations lets a conversation's agent run the command itself.
// Elsewhere in a conversation the agent changes nothing, it proposes, and
// the user accepts: a command marked here is one whose change is fine
// made as the user watches, such as a drawing they see change and can
// undo, or one that only reads. ws adds it to what the agent may run, and
// tells it so. Mark nothing that reaches outside the workspace on the
// user's behalf (a comment posted, an issue moved): that's a proposal
// (p.Proposes).
func (c *Command) Conversations() *Command {
	c.conversations = true
	return c
}

// Label is what the command is doing, as a conversation shows an
// agent's step that ran it, in place of the command as it was typed:
// "Pulling up {1}", where {1} is its first argument, {2} its second, and
// so on. Words for someone watching, not a description of the command:
// "Pulling up sketch-5" for ws sketch show sketch-5 -f llm. A step whose
// command is missing an argument the label names shows the command.
func (c *Command) Label(label string) *Command {
	c.label = label
	return c
}

// Timeout is how long the command may run, in place of the plugin's
// CommandTimeout. 0 is as long as it likes: for one that runs until it's
// stopped (Ctrl-C ends its Context).
func (c *Command) Timeout(d time.Duration) *Command {
	c.timeout = &d
	return c
}

func (c *Command) usage() string { return c.args }

// The formats a command's output is printed in.
const (
	FormatTable = "table" // for a person: the default
	FormatJSON  = "json"
	FormatLLM   = "llm" // markdown, for an agent
)

// Call is one run of a command.
type Call struct {
	ctx     context.Context
	p       *Plugin
	cmd     *Command
	id      string // ws's name for this run
	args    []string
	flags   map[string]bool
	options map[string]string
	format  string
	dir     string
	term    protocol.Terminal
	input   string // protocol.InputPiped, InputTerminal, or "": nothing to read
	in      io.Reader
	out     io.Writer
	err     io.Writer
}

// Context ends when the person presses Ctrl-C, or the command runs out
// of time.
func (c *Call) Context() context.Context { return c.ctx }

// Args is what followed the command's name, flags aside.
func (c *Call) Args() []string { return c.args }

// Arg is the i'th argument, or "" if there's none.
func (c *Call) Arg(i int) string {
	if i < len(c.args) {
		return c.args[i]
	}
	return ""
}

// Flag is whether a flag declared with Command.Flag was given.
func (c *Call) Flag(name string) bool { return c.flags[name] }

// Option is the value of a flag declared with Command.Option, or "".
func (c *Call) Option(name string) string { return c.options[name] }

// Format is the format asked for: FormatTable, FormatJSON or FormatLLM.
// Say, List and Show print in it without being asked.
func (c *Call) Format() string { return c.format }

// Dir is the folder the command was run from.
func (c *Call) Dir() string { return c.dir }

// Width is how wide the terminal on the other end is, or 0 if it isn't
// one.
func (c *Call) Width() int {
	if !c.term.TTY {
		return 0
	}
	return c.term.Width
}

// Stdout is the command's output, for what Say, List and Show don't
// cover.
func (c *Call) Stdout() io.Writer { return c.out }

// Stderr is the command's error output.
func (c *Call) Stderr() io.Writer { return c.err }

// Stdin is the command's standard input: what was piped to it (ws sketch
// edit sketch-4 < ops.json), or the person typing at the terminal. It
// reads from ws as it's read, so a command that doesn't read it never
// waits on it. Nothing to read, from a ws from before it gave commands
// their input. Piped says which. Not os.Stdin: that's the plugin's
// connection to ws.
func (c *Call) Stdin() io.Reader {
	if c.in == nil {
		if c.input == "" {
			c.in = strings.NewReader("")
		} else {
			c.in = &inputReader{c: c}
		}
	}
	return c.in
}

// Piped is whether something was piped to the command: its standard
// input is a pipe or a file, not a terminal. A command that takes its
// input either as an argument or piped asks this before it reads, so it
// doesn't sit waiting for a person who meant to give it an argument.
func (c *Call) Piped() bool { return c.input == protocol.InputPiped }

// inputReader reads a command's standard input from ws, a command/read
// at a time.
type inputReader struct {
	c    *Call
	left []byte // read from ws, not yet from the reader
	eof  bool
	err  error
}

func (r *inputReader) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	for len(r.left) == 0 {
		if r.eof {
			return 0, io.EOF
		}
		if r.err != nil {
			return 0, r.err
		}
		ctx := r.c.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		var got protocol.CommandReadResult
		err := r.c.p.ask(ctx, protocol.MethodCommandRead, protocol.CommandReadParams{ID: r.c.id, Max: min(len(b), protocol.ReadAtOnce)}, &got)
		if err != nil {
			r.err = fmt.Errorf("reading the command's input: %w", err)
			return 0, r.err
		}
		r.left, r.eof = got.Data, got.EOF
		if len(got.Data) == 0 && !got.EOF {
			r.err = errors.New("reading the command's input: ws gave nothing, and didn't say it was the end")
		}
	}
	n := copy(b, r.left)
	r.left = r.left[n:]
	return n, nil
}

// ExitError is an error that ends a command with a code of its choosing.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return e.Err.Error() }
func (e *ExitError) Unwrap() error { return e.Err }

// Exit is err, ending the command with code (a handler's plain error
// ends it with 1).
func Exit(code int, err error) error { return &ExitError{Code: code, Err: err} }

// outputWriter sends what's written to ws as a command's output.
type outputWriter struct {
	p      *Plugin
	id     string
	stream string
}

func (w *outputWriter) Write(b []byte) (int, error) {
	if len(b) > 0 {
		w.p.notify(protocol.MethodCommandOutput, protocol.CommandOutputParams{ID: w.id, Stream: w.stream, Data: string(b)})
	}
	return len(b), nil
}

// runCommand runs what someone typed after the plugin's command, and
// returns its exit code: 0, 1 for a handler's error, 2 for a command or
// flag that isn't known.
func (p *Plugin) runCommand(ctx context.Context, in protocol.CommandRunParams) int {
	out := &outputWriter{p: p, id: in.ID, stream: protocol.Stdout}
	errw := &outputWriter{p: p, id: in.ID, stream: protocol.Stderr}

	args := in.Args
	if len(args) == 0 || isHelp(args[0]) {
		p.help(out)
		return 0
	}
	var cmd *Command
	for _, c := range p.commands {
		if c.name == args[0] {
			cmd = c
		}
	}
	if cmd == nil {
		fmt.Fprintf(errw, "unknown command %q\n\n", args[0])
		p.help(errw)
		return 2
	}
	call := &Call{p: p, cmd: cmd, id: in.ID, dir: in.Dir, term: in.Terminal, input: in.Input, out: out, err: errw, format: FormatTable}
	help, err := call.parse(args[1:])
	if err != nil {
		fmt.Fprintf(errw, "%v\n\n", err)
		p.helpFor(errw, cmd)
		return 2
	}
	if help {
		p.helpFor(out, cmd)
		return 0
	}
	if p.broken != nil {
		fmt.Fprintln(errw, p.broken)
		return 1
	}

	limit := p.CommandTimeout
	if cmd.timeout != nil {
		limit = *cmd.timeout
	}
	if limit > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, limit)
		defer cancel()
	}
	call.ctx = ctx
	err = func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("%s panicked: %v", cmd.name, r)
				p.Log.Error(err.Error())
			}
		}()
		return cmd.fn(call)
	}()
	if err == nil {
		return 0
	}
	if errors.Is(err, context.DeadlineExceeded) {
		err = fmt.Errorf("%s ran out of time (%s)", cmd.name, limit)
	}
	fmt.Fprintln(errw, err)
	var exit *ExitError
	if errors.As(err, &exit) {
		return exit.Code
	}
	return 1
}

func isHelp(arg string) bool { return arg == "help" || arg == "--help" || arg == "-h" }

// parse reads a command's flags and arguments. -f and --format are every
// command's; the rest are what it declared. After "--", everything is an
// argument.
func (c *Call) parse(args []string) (help bool, err error) {
	c.flags, c.options = map[string]bool{}, map[string]string{}
	declared := map[string]flag{}
	for _, f := range c.cmd.flags {
		declared[f.name] = f
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			c.args = append(c.args, args[i+1:]...)
			return help, nil
		case a == "-h" || a == "--help":
			help = true
		case a == "-f" || a == "--format" || strings.HasPrefix(a, "--format=") || strings.HasPrefix(a, "-f="):
			v, ok := strings.CutPrefix(a, "--format=")
			if !ok {
				v, ok = strings.CutPrefix(a, "-f=")
			}
			if !ok {
				if i+1 >= len(args) {
					return help, fmt.Errorf("%s needs a format: table, json or llm", a)
				}
				i++
				v = args[i]
			}
			switch v {
			case FormatTable, FormatJSON, FormatLLM:
				c.format = v
			default:
				return help, fmt.Errorf("unknown format %q: it's table, json or llm", v)
			}
		case strings.HasPrefix(a, "--"):
			name, value, hasValue := strings.Cut(a[2:], "=")
			f, ok := declared[name]
			if !ok {
				return help, fmt.Errorf("unknown flag --%s", name)
			}
			if !f.takesValue {
				if hasValue {
					return help, fmt.Errorf("--%s takes no value", name)
				}
				c.flags[name] = true
				continue
			}
			if !hasValue {
				if i+1 >= len(args) {
					return help, fmt.Errorf("--%s needs a value", name)
				}
				i++
				value = args[i]
			}
			c.options[name] = value
		default:
			c.args = append(c.args, a)
		}
	}
	return help, nil
}

// help prints what the plugin's command does.
func (p *Plugin) help(to io.Writer) {
	w := &strings.Builder{}
	defer func() { io.WriteString(to, w.String()) }() //nolint:errcheck // one piece of output, not five
	name := p.where().Plugin.Name
	if name == "" {
		name = "<plugin>"
	}
	if p.description != "" {
		fmt.Fprintf(w, "%s\n\n", p.description)
	}
	fmt.Fprintf(w, "Usage:\n  ws %s <command> [flags]\n\nCommands:\n", name)
	cmds := append([]*Command(nil), p.commands...)
	sort.Slice(cmds, func(i, j int) bool { return cmds[i].name < cmds[j].name })
	width := 0
	for _, c := range cmds {
		width = max(width, len(c.name))
	}
	for _, c := range cmds {
		fmt.Fprintf(w, "  %-*s  %s\n", width, c.name, c.short)
	}
	fmt.Fprintf(w, "\nFlags:\n  -f, --format  table, json or llm (default table)\n  -h, --help    this help\n")
}

// helpFor prints what one command does and takes.
func (p *Plugin) helpFor(to io.Writer, c *Command) {
	w := &strings.Builder{}
	defer func() { io.WriteString(to, w.String()) }() //nolint:errcheck // one piece of output
	name := p.where().Plugin.Name
	if name == "" {
		name = "<plugin>"
	}
	if c.short != "" {
		fmt.Fprintf(w, "%s\n\n", c.short)
	}
	usage := fmt.Sprintf("ws %s %s", name, c.name)
	if c.args != "" {
		usage += " " + c.args
	}
	fmt.Fprintf(w, "Usage:\n  %s [flags]\n\nFlags:\n", usage)
	type line struct{ flag, usage string }
	lines := []line{}
	for _, f := range c.flags {
		l := "    --" + f.name
		if f.takesValue {
			l += " <value>"
		}
		lines = append(lines, line{l, f.usage})
	}
	lines = append(lines, line{"-f, --format <format>", "table, json or llm (default table)"}, line{"-h, --help", "this help"})
	width := 0
	for _, l := range lines {
		width = max(width, len(l.flag))
	}
	for _, l := range lines {
		fmt.Fprintf(w, "  %-*s  %s\n", width, l.flag, l.usage)
	}
}
