package plugin

import (
	"strings"
	"testing"
	"time"
)

type issue struct {
	Key     string `ws:"Issue"`
	Summary string
	Done    bool
	secret  string //nolint:unused // unexported: never a column
	Hidden  string `ws:"-"`
}

func TestTabulate(t *testing.T) {
	cols, cells, err := tabulate([]*issue{{Key: "PROJ-1", Summary: "Token cache", Done: true, Hidden: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(cols, ","); got != "Issue,Summary,Done" {
		t.Errorf("columns: %s", got)
	}
	if got := strings.Join(cells[0], ","); got != "PROJ-1,Token cache,yes" {
		t.Errorf("cells: %s", got)
	}
	if _, _, err := tabulate("not rows"); err == nil {
		t.Error("something that isn't a slice was tabulated")
	}
}

func TestTextTableFitsTheTerminal(t *testing.T) {
	cols := []string{"Issue", "Summary"}
	cells := [][]string{{"PROJ-1", strings.Repeat("long ", 30)}, {"PROJ-2", "short"}}
	for _, line := range strings.Split(strings.TrimRight(textTable(cols, cells, 40, false), "\n"), "\n") {
		if n := len([]rune(line)); n > 40 {
			t.Errorf("a line is %d wide in a terminal of 40: %q", n, line)
		}
	}
	// No terminal: nothing is cut.
	if out := textTable(cols, cells, 0, false); !strings.Contains(out, strings.Repeat("long ", 30)) {
		t.Errorf("a cell was cut with no terminal to fit:\n%s", out)
	}
	if out := textTable(cols, cells, 0, false); !strings.HasPrefix(out, "ISSUE   SUMMARY") {
		t.Errorf("the header:\n%s", out)
	}
}

func TestMarkdownTable(t *testing.T) {
	got := markdownTable([]string{"A", "B"}, [][]string{{"x|y", "z"}})
	want := "| A | B |\n| --- | --- |\n| x\\|y | z |\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestRelTime(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Second: "just now",
		5 * time.Minute:  "5 minutes ago",
		3 * time.Hour:    "3 hours ago",
		72 * time.Hour:   "3 days ago",
	} {
		if got := relTime(time.Now().Add(-d)); got != want {
			t.Errorf("%s ago is %q, want %q", d, got, want)
		}
	}
	if got := relTime(time.Time{}); got != "never" {
		t.Errorf("no time is %q", got)
	}
}
