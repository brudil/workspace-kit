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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

	mu      sync.Mutex
	seq     int
	notes   []note
	answers map[string]func(json.RawMessage) (any, error)
	arrive  chan struct{} // closed and remade whenever a notification comes
	closed  bool
	started chan struct{}             // closed once the plugin's registered
	things  map[string]protocol.Thing // what it's told ws of its things, by kind and name
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
	ws := &WS{p: p, dir: dir, keep: keep, done: make(chan struct{}), arrive: make(chan struct{}), started: make(chan struct{}), things: map[string]protocol.Thing{}}

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
	close(ws.started)
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
	answer := ws.answers[method]
	ws.mu.Unlock()
	if answer != nil {
		return answer(params)
	}
	switch method {
	case protocol.MethodThingPut, protocol.MethodThingRemove, protocol.MethodThingList:
		return ws.keepThings(method, params)
	}
	if !notifications[method] {
		// A request it wasn't told how to answer: as a ws from before it.
		return nil, rpc.MethodNotFound(method)
	}
	ws.mu.Lock()
	ws.notes = append(ws.notes, note{method, params})
	close(ws.arrive)
	ws.arrive = make(chan struct{})
	ws.mu.Unlock()
	return nil, nil
}

// Plugin is the plugin under test.
func (ws *WS) Plugin() *plugin.Plugin { return ws.p }

// notifications are what a plugin tells ws unasked, which the stand-in
// keeps for Notifications and Wait.
var notifications = map[string]bool{
	protocol.MethodCommandOutput: true,
	protocol.MethodLog:           true,
	protocol.MethodViewChanged:   true,
	protocol.MethodViewBadge:     true,
	protocol.MethodSyncState:     true,
}

// Answer has the stand-in answer a request the plugin sends ws (a
// protocol.Method…) with fn, given the request's params. A request it
// hasn't been told how to answer is answered CodeMethodNotFound, as a ws
// from before that request would: the plugin's ErrUnsupported.
func (ws *WS) Answer(method string, fn func(params json.RawMessage) (any, error)) {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	if ws.answers == nil {
		ws.answers = map[string]func(json.RawMessage) (any, error){}
	}
	ws.answers[method] = fn
}

// NoMethod is what an Answer returns to answer as a ws from before a
// request would: the plugin's ErrUnsupported.
func NoMethod(method string) error { return rpc.MethodNotFound(method) }

// Missions are the missions the stand-in has: what it answers p.Missions
// (those not archived, unless asked for them too) and p.Mission with.
func (ws *WS) Missions(missions ...protocol.MissionDetail) {
	ws.Answer(protocol.MethodMissionList, func(params json.RawMessage) (any, error) {
		var in protocol.MissionListParams
		json.Unmarshal(params, &in) //nolint:errcheck // none is in play only
		out := protocol.MissionListResult{Missions: []protocol.Mission{}}
		for _, m := range missions {
			if in.Archived || m.State != protocol.MissionArchived {
				out.Missions = append(out.Missions, m.Mission)
			}
		}
		return out, nil
	})
	ws.Answer(protocol.MethodMissionShow, func(params json.RawMessage) (any, error) {
		var in protocol.MissionShowParams
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, &rpc.Error{Code: protocol.CodeInvalidParams, Message: err.Error()}
		}
		for _, m := range missions {
			if m.Slug == in.Slug {
				return m, nil
			}
		}
		return nil, &rpc.Error{Code: protocol.CodeNotFound, Message: "there's no mission " + in.Slug}
	})
}

// Shows are what the stand-in shows: what it answers p.Show and p.ShowIn
// with, each found by its Ref and Mission. Anything else is ErrNotFound.
func (ws *WS) Shows(shown ...protocol.Shown) {
	ws.Answer(protocol.MethodShow, func(params json.RawMessage) (any, error) {
		var in protocol.ShowParams
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, &rpc.Error{Code: protocol.CodeInvalidParams, Message: err.Error()}
		}
		for _, s := range shown {
			if s.Ref == in.Ref && s.Mission == in.Mission {
				if s.Body == nil {
					s.Body = []string{}
				}
				if s.Links == nil {
					s.Links = []protocol.ShownLink{}
				}
				if s.Said == nil {
					s.Said = []protocol.Said{}
				}
				return s, nil
			}
		}
		return nil, &rpc.Error{Code: protocol.CodeNotFound, Message: "ws has nothing called " + in.Ref}
	})
}

// Conversations has the stand-in start the conversations the plugin
// asks for (p.StartConversation), w1 then w2 and so on, and returns what
// each was started with, in turn.
func (ws *WS) Conversations() func() []string {
	var mu sync.Mutex
	var started []string
	ws.Answer(protocol.MethodConversationStart, func(params json.RawMessage) (any, error) {
		var in protocol.ConversationStartParams
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, &rpc.Error{Code: protocol.CodeInvalidParams, Message: err.Error()}
		}
		if strings.TrimSpace(in.Message) == "" {
			return nil, &rpc.Error{Code: protocol.CodeInvalidParams, Message: "say something"}
		}
		mu.Lock()
		defer mu.Unlock()
		started = append(started, in.Message)
		id := "w" + strconv.Itoa(len(started))
		return protocol.ConversationStarted{ID: id, Path: "/conversations/" + id}, nil
	})
	return func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), started...)
	}
}

// Repos are the repos the stand-in has: what it answers p.Repos with.
func (ws *WS) Repos(repos ...protocol.Repo) {
	ws.Answer(protocol.MethodRepoList, func(json.RawMessage) (any, error) {
		if repos == nil {
			repos = []protocol.Repo{}
		}
		return protocol.RepoListResult{Repos: repos}, nil
	})
}

// keepThings answers what the plugin tells ws of its things, as ws does:
// a thing is kept when its kind is one the plugin registered, its name
// one of that kind's, and it has something for its kind's title.
func (ws *WS) keepThings(method string, params json.RawMessage) (any, error) {
	<-ws.started // what it registered is what its things are named by
	kind := func(k string) (protocol.Ref, bool) {
		i := slices.IndexFunc(ws.reg.Refs, func(r protocol.Ref) bool { return r.Kind == k })
		if i < 0 {
			return protocol.Ref{}, false
		}
		return ws.reg.Refs[i], true
	}
	invalid := func(err error) error { return &rpc.Error{Code: protocol.CodeInvalidParams, Message: err.Error()} }
	ws.mu.Lock()
	defer ws.mu.Unlock()
	switch method {
	case protocol.MethodThingPut:
		var in protocol.ThingPutParams
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, invalid(err)
		}
		if len(in.Things) > protocol.ThingsAtOnce {
			return nil, invalid(fmt.Errorf("%d things at once: %d is the most", len(in.Things), protocol.ThingsAtOnce))
		}
		out := protocol.ThingPutResult{}
		for _, t := range in.Things {
			why := ""
			r, ok := kind(t.Kind)
			switch {
			case !ok:
				why = "the plugin registered no ref called " + t.Kind
			case !r.Names(t.Ref):
				why = "it isn't one of " + t.Kind + "'s names"
			case r.Typed(protocol.FieldTitle) == "":
				why = t.Kind + " has no title field: there's nothing to call one by"
			case strings.TrimSpace(t.Fields[r.Typed(protocol.FieldTitle)]) == "":
				why = "it has nothing for " + r.Typed(protocol.FieldTitle) + ", its title"
			}
			if why != "" {
				out.Refused = append(out.Refused, protocol.ThingRefused{Kind: t.Kind, Ref: t.Ref, Why: why})
				continue
			}
			// What its kind has no field for isn't kept.
			kept := map[string]string{}
			for name, value := range t.Fields {
				if _, ok := r.Named(name); ok {
					kept[name] = value
				}
			}
			t.Fields = kept
			ws.things[t.Kind+"\x00"+t.Ref] = t
		}
		return out, nil
	case protocol.MethodThingRemove:
		var in protocol.ThingRemoveParams
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, invalid(err)
		}
		for _, ref := range in.Refs {
			delete(ws.things, in.Kind+"\x00"+ref)
		}
		return struct{}{}, nil
	}
	var in protocol.ThingListParams
	if err := json.Unmarshal(params, &in); err != nil {
		return nil, invalid(err)
	}
	out := protocol.ThingListResult{Refs: []string{}}
	for _, t := range ws.things {
		if t.Kind == in.Kind {
			out.Refs = append(out.Refs, t.Ref)
		}
	}
	slices.Sort(out.Refs)
	return out, nil
}

// Things are what the plugin has told ws of its things of a kind (a
// Ref's Mirror, Put and Remove), by name: what ws would find and draw
// without asking it.
func (ws *WS) Things(kind string) []protocol.Thing {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	out := []protocol.Thing{}
	for _, t := range ws.things {
		if t.Kind == kind {
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b protocol.Thing) int { return strings.Compare(a.Ref, b.Ref) })
	return out
}

// Show asks the plugin what one of its things is, as ws does when a name
// it registered is shown (ws show RAM-123, the card under a name): kind
// is the Ref's, name the thing's as written. A name the plugin has
// nothing by is plugin.ErrNotFound.
func (ws *WS) Show(kind, name string, full bool) (protocol.RefShowResult, error) {
	var out protocol.RefShowResult
	err := ws.conn.Call(context.Background(), protocol.MethodRefShow, protocol.RefShowParams{Kind: kind, Ref: name, Full: full}, &out)
	var e *rpc.Error
	if errors.As(err, &e) && e.Code == protocol.CodeNotFound {
		return out, fmt.Errorf("%s: %w", e.Message, plugin.ErrNotFound)
	}
	return out, err
}

// Act runs one of the plugin's own actions, as ws does when it's chosen
// in Find, with what's been answered so far to what it asked: an answer
// with an Ask is the action wanting one more.
func (ws *WS) Act(id string, answers ...string) (protocol.ActionRunResult, error) {
	return ws.act(protocol.ActionRunParams{ID: id, Answers: answers})
}

// ActOn runs one of a kind's actions on one of its things, by name.
func (ws *WS) ActOn(kind, name, id string, answers ...string) (protocol.ActionRunResult, error) {
	return ws.act(protocol.ActionRunParams{ID: id, Kind: kind, Ref: name, Answers: answers})
}

func (ws *WS) act(in protocol.ActionRunParams) (protocol.ActionRunResult, error) {
	if in.Answers == nil {
		in.Answers = []string{}
	}
	var out protocol.ActionRunResult
	return out, ws.conn.Call(context.Background(), protocol.MethodActionRun, in, &out)
}

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

// Look says who's looking at what the plugin shows, as ws does when it
// changes, and returns once the plugin has taken it.
func (ws *WS) Look(who plugin.Looking) error {
	if err := ws.conn.Notify(protocol.MethodViewLooking, protocol.ViewLookingParams{App: who.App, WS: who.WS || who.App}); err != nil {
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
