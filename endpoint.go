package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
)

// An endpoint is something the app asks the plugin, declared with what it
// takes and what it answers, as Go types sent as JSON:
//
//	plugin.Get(p, "/api/issues/{key}", func(r *http.Request) (Issue, error) {
//		return find(r.Context(), r.PathValue("key"))
//	})
//	plugin.Post(p, "/api/issues/{key}/comment", func(r *http.Request, in NewComment) (Issue, error) {
//		return comment(r.Context(), r.PathValue("key"), in.Text)
//	})
//
// The framework reads the body and writes the answer. `go tool apigen`
// (cmd/apigen) reads the same declarations and writes the app their types
// in TypeScript, with which endpoint takes and answers which, so the app
// asks through a client that knows them (@withworkspace/ui-lib's
// endpoints) and neither side keeps the other's types by hand.
//
// The path is ServeMux's, without the method: "/api/issues/{key}". The
// request is the handler's to read the rest from: r.PathValue("key"),
// r.Context(). One that's read with a query (what's after the ?) says the
// query's type too: Query.
//
// What's taken and answered is an exported type with a name, a slice,
// map or pointer of one, or a plain value (string, int, bool). struct{}
// is nothing: an endpoint that takes it is sent no body, and one that
// answers it answers 204. A nil slice is answered as [], and a nil map as
// {}, as their types say; one inside a struct is sent as null, so fill it
// or mark it omitempty.
//
// An error is answered as its text, with 500, or with the status it says
// (Status). ErrNotFound is a 404. A body that isn't what's taken is a
// 400.
//
// Each is declared where it's called, with its path as a constant: apigen
// reads the source, and can't see through a function of the plugin's own
// that declares for it. Anything that isn't JSON both ways (a file, a
// stream, a webhook) is a handler: p.Handle.
//
// These are functions, not methods of Plugin, because Go gives a method
// no types of its own.

// Get declares something the app reads.
func Get[Out any](p *Plugin, path string, fn func(r *http.Request) (Out, error)) {
	endpoint(p, "Get", http.MethodGet, path, func(r *http.Request, _ struct{}) (Out, error) { return fn(r) })
}

// Delete declares something the app removes.
func Delete[Out any](p *Plugin, path string, fn func(r *http.Request) (Out, error)) {
	endpoint(p, "Delete", http.MethodDelete, path, func(r *http.Request, _ struct{}) (Out, error) { return fn(r) })
}

// Post declares something the app sends: in is the body it sent.
func Post[In, Out any](p *Plugin, path string, fn func(r *http.Request, in In) (Out, error)) {
	endpoint(p, "Post", http.MethodPost, path, fn)
}

// Put is Post for a PUT: something the app puts in place of what's there.
func Put[In, Out any](p *Plugin, path string, fn func(r *http.Request, in In) (Out, error)) {
	endpoint(p, "Put", http.MethodPut, path, fn)
}

// Patch is Post for a PATCH: a change to part of something.
func Patch[In, Out any](p *Plugin, path string, fn func(r *http.Request, in In) (Out, error)) {
	endpoint(p, "Patch", http.MethodPatch, path, fn)
}

// Status is err as an endpoint answers it: with that HTTP status, not
// 500.
//
//	return Issue{}, plugin.Status(http.StatusConflict, err)
//
// An error of the plugin's own says its status the same way, with a
// method HTTPStatus() int.
func Status(code int, err error) error {
	if err == nil {
		return nil
	}
	return &statusError{code: code, err: err}
}

type statusError struct {
	code int
	err  error
}

func (e *statusError) Error() string   { return e.err.Error() }
func (e *statusError) Unwrap() error   { return e.err }
func (e *statusError) HTTPStatus() int { return e.code }

// statusOf is the status an endpoint's error is answered with.
func statusOf(err error) int {
	var says interface{ HTTPStatus() int }
	switch {
	case errors.As(err, &says):
		if code := says.HTTPStatus(); code >= 400 && code <= 599 {
			return code
		}
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

// endpoint declares a handler that reads In from the body and answers
// with Out.
func endpoint[In, Out any](p *Plugin, name, method, path string, fn func(r *http.Request, in In) (Out, error)) {
	p.declaring(name)
	if !strings.HasPrefix(path, "/") {
		p.declErrs = append(p.declErrs, fmt.Errorf("plugin.%s(p, %q, …): an endpoint's path starts with /, and has no method in it", name, path))
		return
	}
	takes := reflect.TypeFor[In]() != nothing
	answers := reflect.TypeFor[Out]() != nothing
	pattern := method + " " + path
	p.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in In
		if takes {
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				why := err.Error()
				if errors.Is(err, io.EOF) {
					why = "it was sent no body"
				}
				http.Error(w, fmt.Sprintf("%s takes JSON: %s", pattern, why), http.StatusBadRequest)
				return
			}
		}
		out, err := fn(r, in)
		if err != nil {
			http.Error(w, err.Error(), statusOf(err))
			return
		}
		if !answers {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		data, err := encode(out)
		if err != nil {
			p.Log.Error(fmt.Sprintf("%s: its answer couldn't be written as JSON: %v", pattern, err))
			http.Error(w, fmt.Sprintf("%s: its answer couldn't be written as JSON: %v", pattern, err), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(data) //nolint:errcheck // the app has gone
	}))
}

// nothing is the type of an endpoint that takes or answers nothing.
var nothing = reflect.TypeFor[struct{}]()

// encode is an answer as JSON. A slice or map that's nil is written as
// empty, not null: its type in the app is an array, or a record.
func encode(out any) ([]byte, error) {
	if v := reflect.ValueOf(out); v.IsValid() {
		switch {
		case v.Kind() == reflect.Slice && v.IsNil() && v.Type().Elem().Kind() != reflect.Uint8:
			return []byte("[]\n"), nil
		case v.Kind() == reflect.Map && v.IsNil():
			return []byte("{}\n"), nil
		}
	}
	data, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
