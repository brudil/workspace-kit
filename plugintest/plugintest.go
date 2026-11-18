// Package plugintest runs a plugin inside a test, against a stand-in for
// ws: it starts the plugin, and then does what ws does to one. It runs a
// command, asks the plugin's HTTP what its app would, changes config, and
// shows what the plugin told ws.
//
//	func TestSay(t *testing.T) {
//		ws := plugintest.New(t, files, setup)
//		got := ws.Run("say")
//		if got.Stdout != "Hello, workspace\n" {
//			t.Errorf("say printed %q", got.Stdout)
//		}
//	}
//
// setup is whatever declares the plugin: what its main does between
// plugin.New and Run.
package plugintest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/withworkspace/plugin"
	"github.com/withworkspace/plugin/internal/rpc"
	"github.com/withworkspace/plugin/protocol"
)

// WS is the stand-in for ws that a plugin under test is running against.
type WS struct {
	// Terminal is what commands are told is on the other end. The zero
	// value is no terminal: nothing is cut to fit.
	Terminal protocol.Terminal

	p    *plugin.Plugin
	conn *rpc.Conn
	reg  protocol.Registration
	ver  int // the protocol version the handshake chose
	dir  string
	keep bool          // dir is the test's own: not removed
	done chan struct{} // closed when the plugin stops

	mu     sync.Mutex
	seq    int
	notes  []note
	arrive chan struct{} // closed and remade whenever a notification comes
	closed bool
}

// note is a notification the plugin sent.
type note struct {
	method string
	params json.RawMessage
}

// Option says how the plugin is started.
type Option func(*options)

type options struct {
	name   string
	config map[string]any
	dir    string
	root   string
	ws     string
	offer  []int
}

// Config is the config the plugin starts with, by key.
func Config(values map[string]any) Option { return func(o *options) { o.config = values } }

// Dir is the folder the plugin runs in: its data folder is dir/data. It's
// kept when the plugin stops, so a second plugin started in it finds what
// the first left. The default is a temporary folder of the plugin's own.
func Dir(dir string) Option { return func(o *options) { o.dir = dir } }

// Root is the workspace's folder, as the plugin's told (the default is the
// folder the plugin runs in).
func Root(dir string) Option { return func(o *options) { o.root = dir } }

// Executable is the program the plugin's told is ws: what it runs for a
// ws command (p.WS). A test gives it a script that answers as ws would.
// The default is none, and p.WS fails with plugin.ErrNoWS.
func Executable(path string) Option { return func(o *options) { o.ws = path } }

// Offer is the protocol versions the stand-in for ws says it speaks, in
// place of every version this release of the framework does: to try a
// plugin against a ws that's older or newer than it.
func Offer(versions ...int) Option { return func(o *options) { o.offer = versions } }

// Name is the plugin's name (the default is "test").
func Name(name string) Option { return func(o *options) { o.name = name } }

// New starts a plugin for a test, failing the test if it doesn't start.
// It's stopped when the test ends.
func New(t testing.TB, files fs.FS, setup func(p *plugin.Plugin), opts ...Option) *WS {
	t.Helper()
	ws, err := Start(files, setup, opts...)
	if err != nil {
		t.Fatalf("starting the plugin: %v", err)
	}
	t.Cleanup(ws.Close)
	return ws
}

// Start starts a plugin and returns why it didn't, if it didn't: for a
// test of a plugin that shouldn't start. Close it when done.
func Start(files fs.FS, setup func(p *plugin.Plugin), opts ...Option) (*WS, error) {
	o := options{name: "test"}
	for _, opt := range opts {
		opt(&o)
	}
	dir, keep := o.dir, o.dir != ""
	if !keep {
		var err error
		if dir, err = os.MkdirTemp("", "plugintest-"); err != nil {
			return nil, err
		}
	}
	p := plugin.New(files)
	if setup != nil {
		setup(p)
	}
	ws := &WS{p: p, dir: dir, keep: keep, done: make(chan struct{}), arrive: make(chan struct{})}

	toPlugin, fromWS := io.Pipe()
	toWS, fromPlugin := io.Pipe()
	go func() {
		p.Serve(toPlugin, fromPlugin) //nolint:errcheck // a pipe's end
		fromPlugin.Close()
		close(ws.done)
	}()
	ws.conn = rpc.NewConn(toWS, fromWS, ws.handle)
	closeWS := func() { fromWS.Close() }
	go func() { <-ws.done; toWS.Close() }()

	data := filepath.Join(dir, "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		closeWS()
		return nil, err
	}
	config, err := encode(o.config)
	if err != nil {
		closeWS()
		return nil, err
	}
	root := o.root
	if root == "" {
		root = dir
	}
	offer := o.offer
	if len(offer) == 0 {
		offer = protocol.Versions
	}
	in := protocol.InitializeParams{
		Protocol:  slices.Max(offer),
		Protocols: offer,
		WS:        protocol.WSInfo{Version: "test", Executable: o.ws},
		Plugin:    protocol.PluginInfo{Name: o.name, Version: "0.0.0", Dir: dir, Data: data},
		Workspace: protocol.Workspace{Root: root},
		Config:    config,
		// No socket: requests are answered by the plugin's handler.
		View: protocol.ViewInfo{Base: "/plugins/" + o.name},
	}
	var out protocol.InitializeResult
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := ws.conn.Call(ctx, protocol.MethodInitialize, in, &out); err != nil {
		closeWS()
		if !keep {
			os.RemoveAll(dir) //nolint:errcheck // a temporary folder
		}
		return nil, err
	}
	if out.Protocol == 0 || !slices.Contains(offer, out.Protocol) {
		closeWS()
		if !keep {
			os.RemoveAll(dir) //nolint:errcheck // a temporary folder
		}
		return nil, fmt.Errorf("the plugin chose version %d of the plugin protocol: it speaks %v, and ws was said to speak %v", out.Protocol, out.Protocols, offer)
	}
	ws.reg, ws.ver = out.Registration, out.Protocol
	go func() { <-ws.done; closeWS() }()
	return ws, nil
}

func encode(values map[string]any) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	for k, v := range values {
		data, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("config %s: %w", k, err)
		}
		out[k] = data
	}
	return out, nil
}

// handle takes what the plugin sends ws: notifications, kept for the
// test to read.
func (ws *WS) handle(_ context.Context, method string, params json.RawMessage) (any, error) {
	ws.mu.Lock()
	ws.notes = append(ws.notes, note{method, params})
	close(ws.arrive)
	ws.arrive = make(chan struct{})
	ws.mu.Unlock()
	return nil, nil
}

// Plugin is the plugin under test.
func (ws *WS) Plugin() *plugin.Plugin { return ws.p }

// Protocol is the version of the plugin protocol the handshake chose.
func (ws *WS) Protocol() int { return ws.ver }

// Registration is what the plugin registered.
func (ws *WS) Registration() protocol.Registration { return ws.reg }

// Data is the plugin's data folder: a temporary one.
func (ws *WS) Data() string { return filepath.Join(ws.dir, "data") }

// Result is how a command went.
type Result struct {
	Stdout string
	Stderr string
	Exit   int
}

// Run runs the plugin's command as "ws <plugin> args..." would, and
// returns what it printed and its exit code.
func (ws *WS) Run(args ...string) Result {
	return ws.RunContext(context.Background(), args...)
}

// RunContext is Run for a command that's stopped: ctx ending is Ctrl-C.
// What the command printed before it was stopped is returned, with an
// exit code of -1.
func (ws *WS) RunContext(ctx context.Context, args ...string) Result {
	ws.mu.Lock()
	ws.seq++
	id := fmt.Sprintf("c%d", ws.seq)
	ws.mu.Unlock()
	if args == nil {
		args = []string{}
	}
	var out protocol.CommandRunResult
	err := ws.conn.Call(ctx, protocol.MethodCommandRun,
		protocol.CommandRunParams{ID: id, Args: args, Dir: ws.dir, Terminal: ws.Terminal}, &out)
	res := Result{Exit: out.Exit}
	if err != nil {
		res.Exit, res.Stderr = -1, err.Error()+"\n"
	}
	// Output sent before the answer was handled before the answer.
	ws.mu.Lock()
	defer ws.mu.Unlock()
	for _, n := range ws.notes {
		if n.method != protocol.MethodCommandOutput {
			continue
		}
		var o protocol.CommandOutputParams
		if json.Unmarshal(n.params, &o) != nil || o.ID != id {
			continue
		}
		if o.Stream == protocol.Stderr {
			res.Stderr += o.Data
		} else {
			res.Stdout += o.Data
		}
	}
	return res
}

// Response is an answer from the plugin's HTTP: its app's files, and
// what its app asks it.
type Response struct {
	Status int
	Header http.Header
	Body   string
}

// Get asks the plugin's HTTP for path, as its app would with
// fetch(ws.api(path)): the path under the plugin's base
// ("/api/issues?open=1"), as ws sends it on.
func (ws *WS) Get(path string) Response {
	return ws.serve(httptest.NewRequest(http.MethodGet, path, nil))
}

// Post posts a form to the plugin's HTTP. For any other request (a JSON
// body, another method), serve one with Plugin().Handler().
func (ws *WS) Post(path string, form url.Values) Response {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return ws.serve(r)
}

func (ws *WS) serve(r *http.Request) Response {
	r.Header.Set("X-Forwarded-Prefix", "/plugins/"+ws.p.Name())
	rec := httptest.NewRecorder()
	ws.p.Handler().ServeHTTP(rec, r)
	return Response{Status: rec.Code, Header: rec.Header(), Body: rec.Body.String()}
}

// SetConfig changes the plugin's config to values, the whole of it, and
// returns once the plugin has taken it.
func (ws *WS) SetConfig(values map[string]any) error {
	config, err := encode(values)
	if err != nil {
		return err
	}
	if err := ws.conn.Notify(protocol.MethodConfigChanged, protocol.ConfigChangedParams{Config: config}); err != nil {
		return err
	}
	// A request is answered only after what came before it was handled.
	ws.conn.Call(context.Background(), "$/ping", nil, nil) //nolint:errcheck // nobody answers it: that's the point
	return nil
}

// Notifications are the params of each notification of a method the
// plugin has sent so far ("view/changed", "sync/state", "log", …).
func (ws *WS) Notifications(method string) []json.RawMessage {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	var out []json.RawMessage
	for _, n := range ws.notes {
		if n.method == method {
			out = append(out, n.params)
		}
	}
	return out
}

// Wait waits until the plugin has sent n notifications of a method, and
// returns them, or fewer after five seconds.
func (ws *WS) Wait(method string, n int) []json.RawMessage {
	deadline := time.After(5 * time.Second)
	for {
		got := ws.Notifications(method)
		if len(got) >= n {
			return got
		}
		ws.mu.Lock()
		arrive := ws.arrive
		ws.mu.Unlock()
		select {
		case <-arrive:
		case <-ws.done:
			return ws.Notifications(method)
		case <-deadline:
			return ws.Notifications(method)
		}
	}
}

// Logs are the warnings and errors the plugin has told ws of.
func (ws *WS) Logs() []protocol.LogParams {
	var out []protocol.LogParams
	for _, raw := range ws.Notifications(protocol.MethodLog) {
		var l protocol.LogParams
		if json.Unmarshal(raw, &l) == nil {
			out = append(out, l)
		}
	}
	return out
}

// Done is closed when the plugin stops: told to, or of its own accord
// (config its registration depends on changed).
func (ws *WS) Done() <-chan struct{} { return ws.done }

// Close stops the plugin, as ws does: shutdown, then its stdin closed.
func (ws *WS) Close() {
	ws.mu.Lock()
	if ws.closed {
		ws.mu.Unlock()
		return
	}
	ws.closed = true
	ws.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws.conn.Call(ctx, protocol.MethodShutdown, nil, nil) //nolint:errcheck // it may have stopped already
	select {
	case <-ws.done:
	case <-time.After(5 * time.Second):
	}
	if !ws.keep {
		os.RemoveAll(ws.dir) //nolint:errcheck // a temporary folder
	}
}
