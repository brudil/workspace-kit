package plugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// View is an entry in ws's sidebar and the pages behind it. ws shows the
// pages in a frame of its own, in its own look.
type View struct {
	p     *Plugin
	title string
	icon  string
	under string
	first string // where the entry opens: the first page declared
}

// ViewOption says more about a view.
type ViewOption func(*View)

// Icon is the view's icon in the sidebar: one of ws's icon names.
func Icon(name string) ViewOption { return func(v *View) { v.icon = name } }

// Under puts the view's entry in a group of the sidebar ("tasks").
func Under(group string) ViewOption { return func(v *View) { v.under = group } }

// View declares a view: its title, any options (Icon, Under), and last a
// function that declares its pages.
//
//	p.View("Jira", plugin.Icon("ticket"), plugin.Under("tasks"), func(v *plugin.View) {
//		v.Page("/", "issues.html", listIssues)
//		v.Page("/browse/{ref}", "issue.html", showIssue)
//	})
func (p *Plugin) View(title string, parts ...any) {
	p.declaring("View")
	v := &View{p: p, title: title}
	p.views = append(p.views, v)
	for _, part := range parts {
		switch x := part.(type) {
		case ViewOption:
			x(v)
		case func(*View):
			x(v)
		default:
			panic(fmt.Sprintf("plugin: View(%q) was given a %T: it takes options (plugin.Icon, plugin.Under) and a func(*plugin.View)", title, part))
		}
	}
}

// path is where the view's sidebar entry opens.
func (v *View) path() string {
	if v.first == "" {
		return "/"
	}
	return v.first
}

// Request is a request for a page: an *http.Request, so r.Context(),
// r.PathValue("ref") and r.FormValue("q") are all there.
type Request struct {
	*http.Request
}

// route is one thing the view answers.
type route struct {
	pattern  string
	template string                      // a page's, in views/
	page     func(*Request) (any, error) // a page's data
	post     func(*Request) error        // a form's handler
	handler  http.Handler                // anything else
	view     *View
}

// Page declares a page: a path (as net/http's ServeMux takes them:
// "/browse/{ref}"), a template in the plugin's views folder, and a
// function returning what the template is given.
//
// The template is the page's body alone. The framework wraps it in ws's
// layout. A template named "title" in the same file is the page's title:
//
//	{{define "title"}}{{.Key}} {{.Summary}}{{end}}
//	<h1>{{.Summary}}</h1>
//
// Links are relative to the view ("browse/PROJ-1"), or made with url.
func (v *View) Page(pattern, template string, fn func(r *Request) (any, error)) {
	if v.first == "" && !strings.Contains(pattern, "{") {
		v.first = pattern
	}
	v.p.routes = append(v.p.routes, route{pattern: method("GET", pattern), template: template, page: fn, view: v})
}

// Post declares a form's handler: fn does what the form asks, and the
// person is sent back to where they were (or to the form's "next" field,
// a path in the view).
func (v *View) Post(pattern string, fn func(r *Request) error) {
	v.p.routes = append(v.p.routes, route{pattern: method("POST", pattern), post: fn, view: v})
}

// Handle declares anything else the view serves, as net/http would: an
// API for the page's own script, a download.
func (v *View) Handle(pattern string, h http.Handler) {
	v.p.routes = append(v.p.routes, route{pattern: pattern, handler: h, view: v})
}

// method is pattern with a method before it, if it has none, and "/"
// meaning the view's first page alone.
func method(m, pattern string) string {
	if strings.Contains(pattern, " ") {
		return pattern
	}
	if pattern == "/" {
		pattern = "/{$}"
	}
	return m + " " + pattern
}

// pageSet is the view as built: each page's template, and the mux.
type pageSet struct {
	templates map[string]*template.Template
}

// buildView parses the pages' templates and builds what answers the
// view's requests. An error is the plugin author's: a template that's
// missing or doesn't parse, two pages on one path.
func (p *Plugin) buildView() (err error) {
	if len(p.views) == 0 {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the view's pages: %v", r) // ServeMux panics on patterns that clash
		}
	}()
	p.pages = &pageSet{templates: map[string]*template.Template{}}
	mux := http.NewServeMux()
	var errs []error
	for _, rt := range p.routes {
		rt := rt
		switch {
		case rt.page != nil:
			if _, ok := p.pages.templates[rt.template]; !ok {
				t, err := p.parse(rt.template)
				if err != nil {
					errs = append(errs, err)
					continue
				}
				p.pages.templates[rt.template] = t
			}
			mux.HandleFunc(rt.pattern, func(w http.ResponseWriter, r *http.Request) { p.servePage(w, r, rt) })
		case rt.post != nil:
			mux.HandleFunc(rt.pattern, func(w http.ResponseWriter, r *http.Request) { p.servePost(w, r, rt) })
		default:
			mux.Handle(rt.pattern, rt.handler)
		}
	}
	if p.files != nil {
		if static, err := fs.Sub(p.files, "static"); err == nil {
			if _, err := fs.Stat(p.files, "static"); err == nil {
				mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(static)))
			}
		}
	}
	mux.HandleFunc("POST /-/sync", p.serveSync)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		p.errorPage(w, http.StatusNotFound, "There's nothing here", "No page of this plugin is at "+r.URL.Path+".")
	})
	p.handler = mux
	return errors.Join(errs...)
}

// parse reads a page's template from views/.
func (p *Plugin) parse(name string) (*template.Template, error) {
	if p.files == nil {
		return nil, fmt.Errorf("page template %q: the plugin was given no files (plugin.New(files))", name)
	}
	data, err := fs.ReadFile(p.files, "views/"+name)
	if err != nil {
		return nil, fmt.Errorf("page template views/%s: %w", name, err)
	}
	t, err := template.New(name).Funcs(p.funcs()).Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("page template views/%s: %w", name, err)
	}
	return t, nil
}

// funcs is what a page's template can call.
func (p *Plugin) funcs() template.FuncMap {
	return template.FuncMap{
		// url is a path in the view as an address: {{url "/browse/PROJ-1"}}.
		"url": func(path string) string { return p.base() + "/" + strings.TrimPrefix(path, "/") },
		// reltime is a time as "2 minutes ago".
		"reltime": relTime,
		// pill is a small label for a status: {{pill "Done"}}, or with a
		// tone (good, warn, bad): {{pill "Blocked" "bad"}}.
		"pill": func(text string, tone ...string) template.HTML {
			class := "pill"
			if len(tone) > 0 && tone[0] != "" {
				class += " pill-" + template.HTMLEscapeString(tone[0])
			}
			return template.HTML(`<span class="` + class + `">` + template.HTMLEscapeString(text) + `</span>`)
		},
	}
}

// base is the path ws serves the view under ("/plugins/jira").
func (p *Plugin) base() string { return strings.TrimSuffix(p.where().View.Base, "/") }

// relTime is a time as a person would say it. It takes a time.Time or a
// Unix time in seconds.
func relTime(v any) string {
	var t time.Time
	switch x := v.(type) {
	case time.Time:
		t = x
	case *time.Time:
		if x == nil {
			return ""
		}
		t = *x
	case int64:
		t = time.Unix(x, 0)
	case int:
		t = time.Unix(int64(x), 0)
	default:
		return fmt.Sprint(v)
	}
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	future := d < 0
	if future {
		d = -d
	}
	var s string
	switch {
	case d < 45*time.Second:
		return "just now"
	case d < 90*time.Second:
		s = "a minute"
	case d < time.Hour:
		s = fmt.Sprintf("%d minutes", int(d.Minutes()+0.5))
	case d < 90*time.Minute:
		s = "an hour"
	case d < 24*time.Hour:
		s = fmt.Sprintf("%d hours", int(d.Hours()+0.5))
	case d < 48*time.Hour:
		s = "a day"
	case d < 30*24*time.Hour:
		s = fmt.Sprintf("%d days", int(d.Hours()/24+0.5))
	default:
		return t.Local().Format("2 Jan 2006")
	}
	if future {
		return "in " + s
	}
	return s + " ago"
}

// layout wraps every page: ws's stylesheet and the page's side of the
// frame's messages, both served by ws. The script isn't deferred: it gives
// the page ws's theme before the page first paints.
var layout = template.Must(template.New("layout").Funcs(template.FuncMap{"reltime": relTime}).Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<base href="{{.Base}}/">
<title>{{.Title}}</title>
<link rel="stylesheet" href="/plugin-kit.css">
<script src="/plugin-kit.js"></script>
</head>
<body class="ws-plugin">
<main>
{{if .Error}}<div class="ws-error"><h1>{{.Title}}</h1><p>{{.Error}}</p></div>{{else}}{{.Body}}{{end}}
</main>
{{if .Syncs}}<footer class="ws-sync">
{{range .Syncs}}<span class="ws-sync-state{{if .Error}} ws-sync-failed{{end}}" title="{{.Name}}">{{if .Running}}Syncing {{.Name}}…{{else if .Error}}{{.Name}} couldn't sync: {{.Error}}{{else if .At.IsZero}}{{.Name}} hasn't synced yet{{else}}{{.Name}} as of {{reltime .At}}{{end}}</span>
{{end}}<form method="post" action="-/sync"><button type="submit">Refresh</button></form>
</footer>{{end}}
</body>
</html>
`))

// layoutData is what the layout is given.
type layoutData struct {
	Base  string
	Title string
	Body  template.HTML
	Error string
	Syncs []SyncState
}

func (p *Plugin) writeLayout(w http.ResponseWriter, status int, d layoutData) {
	d.Base = p.base()
	if d.Error == "" {
		d.Syncs = p.SyncStates()
	}
	var buf bytes.Buffer
	if err := layout.Execute(&buf, d); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(buf.Bytes()) //nolint:errcheck // the page went away
}

// errorPage is a page saying what went wrong, in ws's look.
func (p *Plugin) errorPage(w http.ResponseWriter, status int, title, message string) {
	p.writeLayout(w, status, layoutData{Title: title, Error: message})
}

// usable says whether pages can be served, writing the page that says
// why not if they can't.
func (p *Plugin) usable(w http.ResponseWriter) bool {
	if p.broken != nil {
		p.errorPage(w, http.StatusServiceUnavailable, "This plugin couldn't start", p.broken.Error())
		return false
	}
	return true
}

// servePage renders a page: its handler's data, in its template, in the
// layout.
func (p *Plugin) servePage(w http.ResponseWriter, r *http.Request, rt route) {
	if !p.usable(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), p.PageTimeout)
	defer cancel()
	r = r.WithContext(ctx)

	var data any
	err := p.recovering(rt.pattern, func() (err error) {
		data, err = rt.page(&Request{r})
		return err
	})
	if err != nil {
		p.Log.Error(fmt.Sprintf("page %s: %v", r.URL.Path, err))
		p.errorPage(w, http.StatusInternalServerError, "This page couldn't be shown", err.Error())
		return
	}
	t := p.pages.templates[rt.template]
	var body bytes.Buffer
	if err := t.Execute(&body, data); err != nil {
		p.Log.Error(fmt.Sprintf("page %s: views/%s: %v", r.URL.Path, rt.template, err))
		p.errorPage(w, http.StatusInternalServerError, "This page couldn't be shown", err.Error())
		return
	}
	title := rt.view.title
	if tt := t.Lookup("title"); tt != nil {
		var b bytes.Buffer
		if tt.Execute(&b, data) == nil && strings.TrimSpace(b.String()) != "" {
			title = strings.TrimSpace(b.String())
		}
	}
	p.writeLayout(w, http.StatusOK, layoutData{Title: title, Body: template.HTML(body.String())})
}

// servePost runs a form's handler, and sends the person back.
func (p *Plugin) servePost(w http.ResponseWriter, r *http.Request, rt route) {
	if !p.usable(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), p.PageTimeout)
	defer cancel()
	r = r.WithContext(ctx)
	if err := p.recovering(rt.pattern, func() error { return rt.post(&Request{r}) }); err != nil {
		p.Log.Error(fmt.Sprintf("form %s: %v", r.URL.Path, err))
		p.errorPage(w, http.StatusInternalServerError, "That couldn't be done", err.Error())
		return
	}
	p.back(w, r)
}

// back sends the person to the form's "next" (a path in the view), else
// where they came from, else the view's first page.
func (p *Plugin) back(w http.ResponseWriter, r *http.Request) {
	to := p.base() + "/"
	if next := r.FormValue("next"); strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") {
		to = p.base() + next
	} else if ref := r.Referer(); ref != "" {
		to = ref
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// serveSync is the Refresh on every page: run the syncs now, then show
// the page again.
func (p *Plugin) serveSync(w http.ResponseWriter, r *http.Request) {
	if !p.usable(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), p.SyncTimeout)
	defer cancel()
	p.SyncNow(ctx) //nolint:errcheck // the page shows what went wrong
	p.back(w, r)
}

// recovering runs fn, turning a panic into an error.
func (p *Plugin) recovering(what string, fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%s panicked: %v", what, r)
		}
	}()
	return fn()
}

// listen serves the view on a unix socket, removing one left by a run
// before.
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

// Handler is what answers the view's requests, as ws would send them
// (the path under the view's base). It's for tests: a running plugin
// serves it where ws says.
func (p *Plugin) Handler() http.Handler {
	if p.handler == nil {
		return http.NotFoundHandler()
	}
	return p.handler
}
