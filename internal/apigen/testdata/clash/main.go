// A plugin for apigen's test: two packages with a type of one name.
package main

import (
	"embed"
	"errors"
	"net/http"

	"example.com/clash/team"
	"github.com/withworkspace/plugin"
)

// The app as built, which isn't there: it hasn't been built yet. The
// app's types are written all the same.
//
//go:embed ui/dist
var files embed.FS

// Person is who's signed in.
type Person struct {
	Name string `json:"name"`
}

func main() {
	p := plugin.New(files)
	plugin.Get(p, "/api/me", func(r *http.Request) (Person, error) { return Person{}, errors.New("stub") })
	plugin.Get(p, "/api/team", func(r *http.Request) ([]team.Person, error) { return nil, errors.New("stub") })
	p.Run()
}
