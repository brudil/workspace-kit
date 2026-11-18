package protocol_test

import (
	"slices"
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
