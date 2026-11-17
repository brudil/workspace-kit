// Package plugin is the framework for writing a ws plugin in Go.
//
// A plugin is one process that ws's daemon starts and speaks to over the
// process's stdin and stdout (see PROTOCOL.md). This package speaks for it,
// so a plugin is what it declares and a call to Run:
//
//	//go:embed agents.md migrations views
//	var files embed.FS
//
//	func main() {
//		p := plugin.New(files)
//		greeting := p.Config.String("greeting", "Greeting", plugin.Default("Hello"))
//		p.View("Hello", func(v *plugin.View) {
//			v.Page("/", "index.html", func(r *plugin.Request) (any, error) {
//				return map[string]any{"Greeting": greeting.Get()}, nil
//			})
//		})
//		p.Command("say", "Says the greeting", func(c *plugin.Call) error {
//			return c.Say(greeting.Get() + ", workspace")
//		})
//		p.Run()
//	}
//
// Declaring a thing and handling it are one call, and what ws is told the
// plugin adds (its registration) is written from those calls.
package plugin

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/withworkspace/plugin/internal/rpc"
	"github.com/withworkspace/plugin/protocol"
)

// Plugin is a plugin: what it declares before Run, and what it's running
// with after.
type Plugin struct {
	// Config is the plugin's config: keys are declared on it, and read
	// through what declaring them returns.
	Config *Config
	// Log is the plugin's log. Its lines go to the plugin's log file, and
	// warnings and errors to ws as well.
	Log *slog.Logger
	// CommandTimeout is how long a command may run (default 2 minutes);
	// PageTimeout a page (30 seconds); SyncTimeout a sync (5 minutes).
	CommandTimeout time.Duration
	PageTimeout    time.Duration
	SyncTimeout    time.Duration

	files fs.FS

	// What's declared.
	description string
	commands    []*Command
	views       []*View
	routes      []route
	syncs       []*syncer
	setups      []func()
	onConfig    []func()
	guide       string
	guideSet    bool
	roles       []string
	declErrs    []error

	mu       sync.Mutex
	started  bool // Run has answered initialize: nothing more can be declared
	inSetup  bool
	info     protocol.InitializeParams
	conn     *rpc.Conn
	db       *sql.DB
	broken   error // why nothing can run: a migration failed
	listener net.Listener
	server   *http.Server
	handler  http.Handler
	pages    *pageSet

	running     sync.WaitGroup // commands being run
	initialized chan struct{}  // closed once initialize has been answered for

	ctx    context.Context // ends when the plugin stops
	cancel context.CancelFunc
	done   chan struct{} // closed to stop Serve
	once   sync.Once
}

// New is a plugin whose files (agents.md, migrations/, views/, static/)
// are in files: the plugin's folder, built into its executable with
// go:embed. files may be nil for a plugin with none of them.
func New(files fs.FS) *Plugin {
	ctx, cancel := context.WithCancel(context.Background())
	p := &Plugin{
		files:          files,
		CommandTimeout: 2 * time.Minute,
		PageTimeout:    30 * time.Second,
		SyncTimeout:    5 * time.Minute,
		ctx:            ctx,
		cancel:         cancel,
		done:           make(chan struct{}),
		initialized:    make(chan struct{}),
	}
	p.Config = &Config{p: p, values: map[string]json.RawMessage{}}
	p.Log = slog.New(&logHandler{p: p, text: slog.NewTextHandler(os.Stderr, nil)})
	return p
}

// Describe says what the plugin is, in a line, for ws plugin list.
func (p *Plugin) Describe(text string) {
	p.declaring("Describe")
	p.description = text
}

// Setup is run once initialize has brought the config, before the
// registration is given: it's where to declare what depends on config.
// When a config key read in fn later changes, the plugin exits and ws
// starts it afresh, so what was registered from it is registered again.
func (p *Plugin) Setup(fn func()) {
	p.declaring("Setup")
	p.setups = append(p.setups, fn)
}

// OnConfig is run after the config changes while the plugin runs. Values
// read through their keys are already the new ones.
func (p *Plugin) OnConfig(fn func()) {
	p.declaring("OnConfig")
	p.onConfig = append(p.onConfig, fn)
}

// Guide is what agents are told of the plugin: markdown, and the roles
// it's for (none means every role). A plugin whose files hold agents.md
// needs no call: that's its guide. Guide("", roles...) keeps the file's
// text and names roles.
func (p *Plugin) Guide(text string, roles ...string) {
	p.declaring("Guide")
	if text != "" {
		p.guide, p.guideSet = text, true
	}
	p.roles = roles
}

// declaring panics if the plugin is running: what a plugin adds is said
// once, as it starts.
func (p *Plugin) declaring(what string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.started && !p.inSetup {
		panic("plugin: " + what + " after Run has started: declare everything before Run, or in Setup")
	}
}

// Run speaks the protocol on stdin and stdout until ws says to stop, and
// then exits. With --registration as the only argument it prints what the
// plugin would register, and exits.
func (p *Plugin) Run() {
	if len(os.Args) == 2 && os.Args[1] == "--registration" {
		reg, err := p.Registration()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(reg) //nolint:errcheck // stdout
		os.Exit(0)
	}
	// Stdout is the connection's alone: anything the plugin's own code
	// prints goes to its log instead.
	out := os.Stdout
	os.Stdout = os.Stderr
	if err := p.Serve(os.Stdin, out); err != nil {
		fmt.Fprintln(os.Stderr, "plugin:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

// Serve speaks the protocol on r and w until the other side says to stop
// or r ends. Run is Serve on stdin and stdout; a test serves on pipes.
func (p *Plugin) Serve(r io.Reader, w io.Writer) error {
	conn := rpc.NewConn(r, w, p.handle)
	p.mu.Lock()
	p.conn = conn
	p.mu.Unlock()
	select {
	case <-conn.Done():
	case <-p.done:
	}
	p.stop()
	return conn.Err()
}

// stop ends what the plugin holds: its handlers, its view, its database.
func (p *Plugin) stop() {
	p.cancel()
	p.mu.Lock()
	server, db := p.server, p.db
	p.server, p.db = nil, nil
	p.mu.Unlock()
	if server != nil {
		server.Close() //nolint:errcheck // stopping
	}
	if db != nil {
		db.Close() //nolint:errcheck // stopping
	}
}

// exit stops Serve, once.
func (p *Plugin) exit() { p.once.Do(func() { close(p.done) }) }

// handle answers what ws asks.
func (p *Plugin) handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case protocol.MethodInitialize:
		var in protocol.InitializeParams
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, &rpc.Error{Code: protocol.CodeInvalidParams, Message: err.Error()}
		}
		return p.initialize(in)
	case protocol.MethodConfigChanged:
		var in protocol.ConfigChangedParams
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, err
		}
		p.configChanged(in.Config)
		return nil, nil
	case protocol.MethodCommandRun:
		var in protocol.CommandRunParams
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, &rpc.Error{Code: protocol.CodeInvalidParams, Message: err.Error()}
		}
		// Nothing runs before the plugin's ready for it.
		select {
		case <-p.initialized:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		p.running.Add(1)
		defer p.running.Done()
		return protocol.CommandRunResult{Exit: p.runCommand(ctx, in)}, nil
	case protocol.MethodShutdown:
		// Commands being run get a moment to finish and be answered.
		finished := make(chan struct{})
		go func() { p.running.Wait(); close(finished) }()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
		}
		return rpc.Then{Result: nil, Do: p.exit}, nil
	}
	return nil, rpc.MethodNotFound(method)
}

// initialize takes what ws says of where the plugin is running, gets it
// ready (its config-dependent declarations, its database, its view), and
// answers with its registration.
func (p *Plugin) initialize(in protocol.InitializeParams) (any, error) {
	p.mu.Lock()
	if p.info.Protocol != 0 {
		p.mu.Unlock()
		return nil, errors.New("initialize was already sent")
	}
	if in.Protocol == 0 {
		in.Protocol = protocol.Version
	}
	p.info = in
	p.mu.Unlock()
	defer close(p.initialized)
	p.Config.set(in.Config)

	reg, err := p.prepare()
	if err != nil {
		return nil, err
	}
	// The database, if the plugin has migrations. One that can't be
	// brought forward stops everything but the registration: the view and
	// commands say why.
	if p.hasMigrations() {
		if _, err := p.openDB(); err != nil {
			p.broken = err
			p.Log.Error(err.Error())
		}
	}
	if len(p.views) > 0 && in.View.Listen != "" {
		if err := p.listen(in.View.Listen); err != nil {
			return nil, fmt.Errorf("listening for the view on %s: %w", in.View.Listen, err)
		}
	}
	return rpc.Then{
		Result: protocol.InitializeResult{Protocol: protocol.Version, Registration: reg},
		Do:     p.startSyncs, // once ws has the registration
	}, nil
}

// Registration is what the plugin registers, as declared so far. Run
// answers initialize with it; a test or --registration can ask for it
// without a connection.
func (p *Plugin) Registration() (protocol.Registration, error) { return p.prepare() }

// prepare runs the Setups and builds what the declarations need (the
// pages' templates), then closes declaring. It's run once.
func (p *Plugin) prepare() (protocol.Registration, error) {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		if len(p.declErrs) > 0 {
			return protocol.Registration{}, errors.Join(p.declErrs...)
		}
		return p.registration(), nil
	}
	p.started, p.inSetup = true, true
	p.mu.Unlock()
	for _, fn := range p.setups {
		fn()
	}
	p.mu.Lock()
	p.inSetup = false
	p.mu.Unlock()

	if !p.guideSet && p.files != nil {
		if data, err := fs.ReadFile(p.files, "agents.md"); err == nil {
			p.guide = string(data)
		}
	}
	if err := p.buildView(); err != nil {
		p.declErrs = append(p.declErrs, err)
	}
	p.addSyncCommand()
	if len(p.declErrs) > 0 {
		return protocol.Registration{}, errors.Join(p.declErrs...)
	}
	return p.registration(), nil
}

// registration is what's been declared, as ws is told it.
func (p *Plugin) registration() protocol.Registration {
	reg := protocol.Registration{Description: p.description, ConfigVersion: p.Config.version}
	for _, v := range p.views {
		reg.Views = append(reg.Views, protocol.View{Title: v.title, Icon: v.icon, Under: v.under, Path: v.path()})
	}
	if len(p.commands) > 0 {
		cmds := &protocol.Commands{Name: p.info.Plugin.Name, Short: p.description}
		for _, c := range p.commands {
			cmds.Subcommands = append(cmds.Subcommands, protocol.Subcommand{Name: c.name, Short: c.short, Args: c.usage()})
		}
		reg.Commands = cmds
	}
	if p.guide != "" {
		reg.Agent = &protocol.Agent{Roles: p.roles, Guide: p.guide}
	}
	for _, s := range p.syncs {
		reg.Syncs = append(reg.Syncs, protocol.Sync{Name: s.name, Every: int(s.every / time.Second)})
	}
	reg.Config = p.Config.spec()
	return reg
}

// configChanged takes the config after a change. If a key that a Setup
// read has changed, what was registered may no longer hold, so the plugin
// exits to be started afresh; otherwise it carries on with the new values.
func (p *Plugin) configChanged(values map[string]json.RawMessage) {
	restart := p.Config.set(values)
	if restart {
		p.Log.Info("config that the registration depends on changed: exiting to start afresh")
		p.exit()
		return
	}
	for _, fn := range p.onConfig {
		p.guard("OnConfig", fn)
	}
	p.wakeSyncs()
	// What a page shows may well follow config: open ones look again.
	p.Changed()
}

// guard runs fn, turning a panic into a logged error.
func (p *Plugin) guard(what string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			p.Log.Error(fmt.Sprintf("%s panicked: %v", what, r))
		}
	}()
	fn()
}

// notify sends ws a notification, if there's a ws to send it to.
func (p *Plugin) notify(method string, params any) {
	p.mu.Lock()
	conn := p.conn
	p.mu.Unlock()
	if conn != nil {
		conn.Notify(method, params) //nolint:errcheck // ws has gone: nothing to tell
	}
}

// Changed tells ws that what the view shows is stale: open pages of it
// are reloaded.
func (p *Plugin) Changed() { p.notify(protocol.MethodViewChanged, struct{}{}) }

// Badge sets the count beside a view's sidebar entry: the view whose
// entry opens at path. 0 removes it.
func (p *Plugin) Badge(path string, n int) {
	p.notify(protocol.MethodViewBadge, protocol.ViewBadgeParams{Path: path, Count: n})
}

// Name is the plugin's name, as ws knows it. Like Dir, Data and Root,
// it's known once the plugin is running.
func (p *Plugin) Name() string { return p.where().Plugin.Name }

// Dir is the plugin's own folder: where its plugin.toml is.
func (p *Plugin) Dir() string { return p.where().Plugin.Dir }

// Data is the folder for whatever the plugin keeps. ws never reads it.
func (p *Plugin) Data() string { return p.where().Plugin.Data }

// Root is the workspace's folder.
func (p *Plugin) Root() string { return p.where().Workspace.Root }

func (p *Plugin) where() protocol.InitializeParams {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.info
}
