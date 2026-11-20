// A plugin for apigen's test: each mistake apigen says.
package main

import (
	"errors"
	"net/http"

	"github.com/withworkspace/plugin"
)

type hidden struct {
	N int `json:"n"`
}

// Shown is exported, and what it's made of isn't.
type Shown struct {
	In inner `json:"in"`
}

type inner struct {
	N int `json:"n"`
}

var errStub = errors.New("stub")

// get is plugin.Get handed on, not called.
var get = plugin.Get[Shown]

func main() {
	p := plugin.New(nil)
	plugin.Get(p, "/api/hidden", func(r *http.Request) (hidden, error) { return hidden{}, errStub })
	plugin.Get(p, "/api/shown", func(r *http.Request) (Shown, error) { return Shown{}, errStub })
	plugin.Post(p, "/api/anon", func(r *http.Request, in struct {
		N int `json:"n"`
	}) (struct{}, error) {
		return struct{}{}, errStub
	})
	declare(p, "/api/declared", func(r *http.Request) (Shown, error) { return Shown{}, errStub })
	list[Shown](p)
	plugin.Get(p, "/api/twice", func(r *http.Request) (string, error) { return "", errStub })
	plugin.Get(p, "/api/twice", func(r *http.Request) (string, error) { return "", errStub })
	p.Run()
}

// declare declares for the plugin: its path isn't known here.
func declare[T any](p *plugin.Plugin, path string, fn func(r *http.Request) (T, error)) {
	plugin.Get(p, path, fn)
}

// list declares with a type of its own.
func list[T any](p *plugin.Plugin) {
	plugin.Get(p, "/api/list", func(r *http.Request) ([]T, error) { return nil, errStub })
}

// Sift is a query with a field a query can't fill.
type Sift struct {
	By    map[string]string `json:"by"`
	Among []Shown           `json:"among"`
}

// sift is plugin.Query handed on, not given to plugin.Get.
var sift = plugin.Query(func(r *http.Request, q Sift) (string, error) { return "", errStub })

func queries(p *plugin.Plugin) {
	plugin.Get(p, "/api/sift", plugin.Query(func(r *http.Request, q Sift) (string, error) { return "", errStub }))
	plugin.Get(p, "/api/sifted", sift)
	plugin.Get(p, "/api/words", plugin.Query(func(r *http.Request, q string) (string, error) { return "", errStub }))
}
