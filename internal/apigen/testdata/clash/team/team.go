// Package team has a Person too.
package team

// Person is someone on the team.
type Person struct {
	Name string `json:"name"`
	Role Role   `json:"role"`
}

// Role is what a person on the team does.
type Role string

const (
	Lead   Role = "lead"
	Member Role = "member"
)
