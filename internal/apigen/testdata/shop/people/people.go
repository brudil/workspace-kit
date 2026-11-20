// Package people is who works at the shop. It declares an endpoint of its
// own.
package people

import (
	"errors"
	"net/http"

	"github.com/withworkspace/plugin"
)

// Person is someone at the shop.
type Person struct {
	Name string `json:"name"`
	Role Role   `json:"role"`
}

// Role is what a person does.
type Role string

const (
	Owner Role = "owner"
	Staff Role = "staff"
)

// Rota is exported, and no endpoint sends it: it isn't written.
type Rota struct {
	On func() []Person
}

// Routes declares what the app asks of people.
func Routes(p *plugin.Plugin) {
	plugin.Get(p, "/api/staff", func(r *http.Request) ([]Person, error) { return nil, errors.New("stub") })
}
