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
	whole := ok
	whole.Fields = []protocol.Field{{Name: "title", Type: protocol.FieldTitle}, {Name: "status", Type: protocol.FieldState}, {Name: "body", Type: protocol.FieldMarkdown}, {Name: "colour", Type: "swatch"}}
	whole.Search = []protocol.Searched{{Field: "title"}, {Field: protocol.FieldKey}, {Field: "body", Max: 100}}
	whole.Views = map[string][]protocol.Block{protocol.ViewCard: {{Block: protocol.BlockTitle, Field: "title"}, {Block: "sparkline", Field: "colour"}, {Block: protocol.BlockSubtitle, Field: protocol.FieldKey}}, "sidebar": {{Block: protocol.BlockTitle, Field: "title"}}}
	if p := whole.Problem(); p != "" {
		t.Errorf("a type, a block and a view this release doesn't know are a newer one's, not a problem: %s", p)
	}
	if whole.Typed(protocol.FieldTitle) != "title" || whole.Typed(protocol.FieldTime) != "" {
		t.Errorf("typed: %q, %q", whole.Typed(protocol.FieldTitle), whole.Typed(protocol.FieldTime))
	}
	if f, ok := whole.Named("status"); !ok || f.Type != protocol.FieldState {
		t.Errorf("named: %+v, %v", f, ok)
	}
	if p := (protocol.Ref{Kind: "issue", Prefixes: []string{"RAM-"}, Tail: "hex"}).Problem(); p != "" {
		t.Errorf("a tail this release doesn't know is a newer one, not a problem: %s", p)
	}
	for why, r := range map[string]protocol.Ref{
		"its kind":               {Kind: "Issue", Prefixes: []string{"RAM-"}},
		"no prefixes":            {Kind: "issue"},
		`prefix "RAM"`:           {Kind: "issue", Prefixes: []string{"RAM"}},         // no hyphen: RAM12 is a word
		`prefix "-"`:             {Kind: "issue", Prefixes: []string{"-"}},           // nothing before it
		`prefix "t#"`:            {Kind: "issue", Prefixes: []string{"t#"}},          // ws's own: a pull request
		`prefix "a/b-"`:          {Kind: "issue", Prefixes: []string{"a/b-"}},        // ws's own: a file
		`prefix "9a-"`:           {Kind: "issue", Prefixes: []string{"RAM-", "9a-"}}, // starts with a digit
		"its address":            {Kind: "issue", Prefixes: []string{"RAM-"}, Address: "https://acme.atlassian.net/browse/"},
		`address "ftp`:           {Kind: "issue", Prefixes: []string{"RAM-"}, Address: "ftp://acme/{ref}"},
		`address "http`:          {Kind: "issue", Prefixes: []string{"RAM-"}, Address: "https://{ref}.atlassian.net/"},
		`address "/`:             {Kind: "issue", Prefixes: []string{"RAM-"}, Address: "/browse/{ref}"},
		"its path":               {Kind: "issue", Prefixes: []string{"RAM-"}, Path: "/issue"},
		"no type":                {Kind: "issue", Prefixes: []string{"RAM-"}, Fields: []protocol.Field{{Name: "title"}}},
		`field "Title"`:          {Kind: "issue", Prefixes: []string{"RAM-"}, Fields: []protocol.Field{{Name: "Title", Type: protocol.FieldTitle}}},
		"2 fields of type state": {Kind: "issue", Prefixes: []string{"RAM-"}, Fields: []protocol.Field{{Name: "title", Type: protocol.FieldTitle}, {Name: "a", Type: protocol.FieldState}, {Name: "b", Type: protocol.FieldState}}},
		`found by "body"`:        {Kind: "issue", Prefixes: []string{"RAM-"}, Search: []protocol.Searched{{Field: "body"}}},
		`path "//`:               {Kind: "issue", Prefixes: []string{"RAM-"}, Path: "//x/{ref}"},
	} {
		if p := r.Problem(); !strings.Contains(p, why) {
			t.Errorf("%+v: %q, want it to say %q", r, p, why)
		}
	}
}

func TestARefNamesItsPrefixesAndANumber(t *testing.T) {
	r := protocol.Ref{Kind: "issue", Prefixes: []string{"RAM-", "RAP-"}}
	for _, name := range []string{"RAM-1", "RAP-123456789"} {
		if !r.Names(name) {
			t.Errorf("%s isn't one of its names", name)
		}
	}
	for _, name := range []string{"", "RAM-", "RAM-0", "RAM-01", "ram-1", "XRAM-1", "RAM-1x", "RAM-1234567890", "RAM-1 ", "SPRINT-1"} {
		if r.Names(name) {
			t.Errorf("%q is one of its names", name)
		}
	}
	r.Tail = "hex"
	if r.Names("RAM-1") {
		t.Error("a kind whose tail isn't known names RAM-1")
	}
}
