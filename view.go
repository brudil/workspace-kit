package plugin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/withworkspace/plugin/protocol"
)

// A plugin's web UI is one client-side app, which ws shows in a page of
// its own. The plugin serves the app's built files and whatever the app
// asks it (its API), and that's all its HTTP is: the framework renders no
// pages, and a plugin has no HTML.

// View is an entry in ws's sidebar: a place in the plugin's app.
type View struct {
	title string
	icon  string
	under string
	path  string // where in the app the entry opens
}

// ViewOption says more about a view.
type ViewOption func(*View)

// Icon is the view's icon in the sidebar: one of ws's icon names.
func Icon(name string) ViewOption { return func(v *View) { v.icon = name } }

// Under puts the view's entry in a group of the sidebar ("tasks").
func Under(group string) ViewOption { return func(v *View) { v.under = group } }

// At is where in the app the view's entry opens ("/board"). Without it,
// the entry opens at "/".
func At(path string) ViewOption { return func(v *View) { v.path = path } }

// View declares an entry in ws's sidebar: its title, and any options
// (Icon, Under, At). It opens the plugin's app (see UI) at the view's
// path, which the app is given and routes itself.
//
//	p.View("Jira", plugin.Icon("ticket"), plugin.Under("tasks"))
//	p.View("Board", plugin.Icon("kanban"), plugin.At("/board"))
func (p *Plugin) View(title string, opts ...ViewOption) {
	p.declaring("View")
	v := &View{title: title, path: "/"}
	for _, opt := range opts {
		opt(v)
	}
	if !strings.HasPrefix(v.path, "/") {
		p.declErrs = append(p.declErrs, fmt.Errorf("the view %q opens at %q: a path in the app starts with /", title, v.path))
	}
	p.views = append(p.views, v)
}

// The app's entry and its stylesheet, as they're named in the folder UI
// is given.
const (
	uiScript = "app.js"
	uiStyle  = "app.css"
)

// UI declares the plugin's app: dir is the folder of the plugin's files
// that holds the app as built ("ui/dist"), with its entry, app.js, and
// its stylesheet, app.css, if it has one.
//
//	//go:embed agents.md migrations ui/dist
//	var files embed.FS
//
//	p := plugin.New(files)
//	p.UI("ui/dist")
//
// The folder is served at /ui/ of the plugin's HTTP, and ws is told to
// load /ui/app.js (and /ui/app.css) in its page for the plugin. Each is
// registered with a hash of what's in it, so a browser asks again when
// the app has been built again. The app is built before the plugin is,
// and its built files are committed: Go alone builds the plugin.
//
// A plugin has one app, whatever views it has, and the app talks to ws
// with @withworkspace/ui-lib.
func (p *Plugin) UI(dir string) {
	p.declaring("UI")
	if p.uiDir != "" {
		p.declErrs = append(p.declErrs, errors.New("UI is declared twice: a plugin has one app"))
		return
	}
	p.uiDir = path.Clean(dir)
}

// route is one thing the plugin's HTTP answers.
type route struct {
	pattern string
	handler http.Handler
}

// Handle declares something the plugin's HTTP answers, as net/http would
// (pattern is ServeMux's: "GET /api/issues/{key}"). It's the app's API:
// the app fetches ws.api("/api/issues") for a handler at /api/issues.
//
// A handler that panics answers 500, and the panic is logged. While the
// plugin's database couldn't be brought up, every handler answers 503
// with why.
func (p *Plugin) Handle(pattern string, h http.Handler) {
	p.declaring("Handle")
	p.routes = append(p.routes, route{pattern: pattern, handler: h})
}

// HandleFunc is Handle for a function.
func (p *Plugin) HandleFunc(pattern string, fn func(http.ResponseWriter, *http.Request)) {
	p.Handle(pattern, http.HandlerFunc(fn))
}

// buildView builds what answers the plugin's HTTP, the app's files and
// its handlers, and what the registration says of the app. An error is
// the plugin author's: an app that isn't built, a view with no app, two
// handlers on one path.
func (p *Plugin) buildView() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the plugin's HTTP: %v", r) // ServeMux panics on patterns that clash
		}
	}()
	if p.uiDir == "" {
		if len(p.views) > 0 {
			return fmt.Errorf("the view %q has no app to show: declare the plugin's app with p.UI (p.UI(\"ui/dist\"))", p.views[0].title)
		}
		if len(p.routes) == 0 {
			return nil
		}
	}
	mux := http.NewServeMux()
	if p.uiDir != "" {
		ui, files, err := p.readUI()
		if err != nil {
			return err
		}
		p.ui = ui
		mux.Handle("GET /ui/{file...}", serveUI(files))
		mux.Handle("GET /ui", http.NotFoundHandler()) // not ServeMux's redirect to /ui/
	}
	for _, rt := range p.routes {
		mux.Handle(rt.pattern, p.guarded(rt))
	}
	p.handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// ServeMux answers a path that isn't clean (/ui/../x) with a
		// redirect, written as HTML. Nothing sends one: it's a 404.
		if clean := path.Clean(r.URL.Path); clean != r.URL.Path && clean+"/" != r.URL.Path {
			http.NotFound(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
	return nil
}

// readUI finds the app in the plugin's files: the folder to serve, and
// its entry and stylesheet as ws is told of them.
func (p *Plugin) readUI() (*protocol.UI, fs.FS, error) {
	if p.files == nil {
		return nil, nil, fmt.Errorf("the app in %s: the plugin was given no files (plugin.New(files))", p.uiDir)
	}
	files, err := fs.Sub(p.files, p.uiDir)
	if err != nil {
		return nil, nil, fmt.Errorf("the app in %s: %w", p.uiDir, err)
	}
	script, err := versioned(files, uiScript)
	if err != nil {
		return nil, nil, fmt.Errorf("the app's entry, %s/%s, isn't in the plugin's files: build the app, and embed %s (//go:embed %s): %w", p.uiDir, uiScript, p.uiDir, p.uiDir, err)
	}
	ui := &protocol.UI{Script: script}
	if style, err := versioned(files, uiStyle); err == nil {
		ui.Style = style
	}
	return ui, files, nil
}

// versioned is a file of the app's as the registration names it: its
// path under /ui/, with a hash of what's in it.
func versioned(files fs.FS, name string) (string, error) {
	data, err := fs.ReadFile(files, name)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "/ui/" + name + "?v=" + hex.EncodeToString(sum[:4]), nil
}

// serveUI serves the app's files, and nothing else: a path that isn't a
// file of the app's is a plain 404, with no listing of a folder.
func serveUI(files fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("file")
		if !fs.ValidPath(name) {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(files, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		// A browser runs a module only if it's served as JavaScript, and
		// applies a stylesheet only as CSS, whatever the machine's own
		// table of types says.
		switch path.Ext(name) {
		case ".js", ".mjs":
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		case ".css":
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		}
		// A file asked for by its hash is that file for good. Any other is
		// asked for again each time.
		if r.URL.Query().Has("v") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	})
}

// guarded is a handler of the plugin's as it's served: not at all while
// the plugin's database couldn't be brought up, and with a panic turned
// into a 500 and logged.
func (p *Plugin) guarded(rt route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p.broken != nil {
			http.Error(w, "This plugin couldn't start: "+p.broken.Error(), http.StatusServiceUnavailable)
			return
		}
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			if rec == http.ErrAbortHandler {
				panic(rec) // the handler's own way of dropping the request
			}
			p.Log.Error(fmt.Sprintf("%s panicked: %v", rt.pattern, rec))
			http.Error(w, fmt.Sprintf("%s panicked: %v", rt.pattern, rec), http.StatusInternalServerError)
		}()
		rt.handler.ServeHTTP(w, r)
	})
}

// listen serves the plugin's HTTP on a unix socket, removing one left by
// a run before.
func (p *Plugin) listen(path string) error {
	os.Remove(path) //nolint:errcheck // usually isn't there
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: p.handler, ReadHeaderTimeout: 10 * time.Second}
	p.mu.Lock()
	p.listener, p.server = ln, server
	p.mu.Unlock()
	go server.Serve(ln) //nolint:errcheck // ends when the plugin stops
	return nil
}

// Handler is what answers the plugin's HTTP, as ws would send it a
// request (the path under the plugin's base). It's for tests: a running
// plugin serves it where ws says.
func (p *Plugin) Handler() http.Handler {
	if p.handler == nil {
		return http.NotFoundHandler()
	}
	return p.handler
}
