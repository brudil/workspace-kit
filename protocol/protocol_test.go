package protocol_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/withworkspace/plugin/protocol"
)

func TestChooseIsTheHighestBothSpeak(t *testing.T) {
	for _, c := range []struct {
		a, b []int
		want int
	}{
		{[]int{1}, []int{1}, 1},
		{[]int{1, 2}, []int{1}, 1},
		{[]int{1}, []int{1, 2}, 1},
		{[]int{1, 2, 3}, []int{2, 3, 4}, 3},
		{[]int{3, 1, 2}, []int{2, 1}, 2}, // in any order
		{[]int{1}, []int{2}, 0},
		{nil, []int{1}, 0},
	} {
		if got := protocol.Choose(c.a, c.b); got != c.want {
			t.Errorf("Choose(%v, %v) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// A ws from before versions were chosen said only its one.
func TestOfferedFromWhatWSSaid(t *testing.T) {
	for _, c := range []struct {
		in   protocol.InitializeParams
		want []int
	}{
		{protocol.InitializeParams{Protocol: 2, Protocols: []int{1, 2}}, []int{1, 2}},
		{protocol.InitializeParams{Protocol: 1}, []int{1}},
		{protocol.InitializeParams{}, []int{1}},
	} {
		if got := c.in.Offered(); !slices.Equal(got, c.want) {
			t.Errorf("%+v: offered %v, want %v", c.in, got, c.want)
		}
	}
}

func TestVersionIsTheNewest(t *testing.T) {
	if protocol.Version != slices.Max(protocol.Versions) {
		t.Errorf("Version is %d, and the newest of Versions %v", protocol.Version, protocol.Versions)
	}
}

func TestCommandNameIsTheWordAfterWS(t *testing.T) {
	for _, c := range []struct {
		r    protocol.Registration
		want string
	}{
		{protocol.Registration{}, ""},
		{protocol.Registration{Commands: &protocol.Commands{}}, "jira"},
		{protocol.Registration{Commands: &protocol.Commands{Name: "issues"}}, "issues"},
	} {
		if got := c.r.CommandName("jira"); got != c.want {
			t.Errorf("%+v: %q, want %q", c.r.Commands, got, c.want)
		}
	}
}

func TestARefsProblem(t *testing.T) {
	ok := protocol.Ref{Kind: "issue", Prefixes: []string{"RAM-", "sc_2-"}, Address: "https://acme.atlassian.net/browse/{ref}", Path: "/issue/{ref}"}
	if p := ok.Problem(); p != "" {
		t.Errorf("%+v: %s", ok, p)
	}
	if p := (protocol.Ref{Kind: "issue", Prefixes: []string{"RAM-"}, Tail: "hex"}).Problem(); p != "" {
		t.Errorf("a tail this release doesn't know is a newer one, not a problem: %s", p)
	}
	for why, r := range map[string]protocol.Ref{
		"its kind":      {Kind: "Issue", Prefixes: []string{"RAM-"}},
		"no prefixes":   {Kind: "issue"},
		`prefix "RAM"`:  {Kind: "issue", Prefixes: []string{"RAM"}},         // no hyphen: RAM12 is a word
		`prefix "-"`:    {Kind: "issue", Prefixes: []string{"-"}},           // nothing before it
		`prefix "t#"`:   {Kind: "issue", Prefixes: []string{"t#"}},          // ws's own: a pull request
		`prefix "a/b-"`: {Kind: "issue", Prefixes: []string{"a/b-"}},        // ws's own: a file
		`prefix "9a-"`:  {Kind: "issue", Prefixes: []string{"RAM-", "9a-"}}, // starts with a digit
		"its address":   {Kind: "issue", Prefixes: []string{"RAM-"}, Address: "https://acme.atlassian.net/browse/"},
		`address "ftp`:  {Kind: "issue", Prefixes: []string{"RAM-"}, Address: "ftp://acme/{ref}"},
		`address "http`: {Kind: "issue", Prefixes: []string{"RAM-"}, Address: "https://{ref}.atlassian.net/"},
		`address "/`:    {Kind: "issue", Prefixes: []string{"RAM-"}, Address: "/browse/{ref}"},
		"its path":      {Kind: "issue", Prefixes: []string{"RAM-"}, Path: "/issue"},
		`path "//`:      {Kind: "issue", Prefixes: []string{"RAM-"}, Path: "//x/{ref}"},
	} {
		if p := r.Problem(); !strings.Contains(p, why) {
			t.Errorf("%+v: %q, want it to say %q", r, p, why)
		}
	}
}
