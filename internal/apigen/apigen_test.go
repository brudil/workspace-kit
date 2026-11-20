package apigen_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/withworkspace/plugin/internal/apigen"
)

var update = flag.Bool("update", false, "write what's generated as what's expected (testdata/*/api.gen.ts)")

// generate runs apigen in one of testdata's plugins, each a module of its
// own built on this framework as it is here.
func generate(t *testing.T, plugin string) (string, error) {
	t.Helper()
	t.Setenv("GOWORK", "off")
	t.Chdir(filepath.Join("testdata", plugin))
	return apigen.Generate()
}

// same fails unless got is the plugin's api.gen.ts (the test has moved
// into the plugin's folder).
func same(t *testing.T, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile("api.gen.ts", []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile("api.gen.ts")
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("what's generated isn't testdata's api.gen.ts (go test ./internal/apigen -update writes it):\n%s", got)
	}
}

// The types the endpoints take and answer are written, with what they're
// made of, and nothing else of the plugin's: shop declares endpoints each
// way one can be, with each kind of type.
func TestWritesWhatTheEndpointsTakeAndAnswer(t *testing.T) {
	got, err := generate(t, "shop")
	if err != nil {
		t.Fatal(err)
	}
	same(t, got)

	// Not what's exported and unsent, what isn't a type, a field that's
	// left out, or a type that writes its own JSON.
	for _, not := range []string{"Unsent", "Audit", "Rota", "Limit", "secret", "idden", "Money", "interface Code", "type Code", "Aisle"} {
		if strings.Contains(got, not) {
			t.Errorf("%q was written, and the app isn't sent it", not)
		}
	}
}

// Two packages' types of one name: the types of the one that isn't the
// plugin's main get its name in front. The plugin's app isn't built (the
// files it embeds aren't there), which is nothing to its types.
func TestTypesOfOneNameGetTheirPackagesInFront(t *testing.T) {
	got, err := generate(t, "clash")
	if err != nil {
		t.Fatal(err)
	}
	same(t, got)
}

// What apigen can't write is said, with where it is, all at once and in
// the order it's written in.
func TestSaysWhatItCantWrite(t *testing.T) {
	_, err := generate(t, "wrong")
	if err == nil {
		t.Fatal("nothing was wrong with it")
	}
	want := strings.Join([]string{
		`main.go:11:6: hidden isn't exported, and it's part of what GET /api/hidden takes or answers: export it (Hidden), so the app has it by name`,
		`main.go:20:6: inner isn't exported, and it's part of what GET /api/shown takes or answers: export it (Inner), so the app has it by name`,
		`main.go:27:18: plugin.Get is used here without being called: apigen reads each endpoint where it's declared, so call it with the endpoint's path and function`,
		`main.go:33:2: POST /api/anon is declared with a struct that has no name: name it, so the app has a name for it too`,
		`main.go:41:2: GET /api/twice is declared twice: the other is at main.go:40:2`,
		`main.go:47:2: plugin.Get's path isn't a constant: apigen reads each endpoint where it's declared, so declare it with its path written out`,
		`main.go:52:2: GET /api/list is declared with a type of the function it's in ([]T): apigen can't tell what that is, so declare each endpoint where its types are known`,
		`main.go:57:2: Sift.By is a map[string]string: a query's field is a string, a number, a bool, a type that reads itself from text, a pointer to one, or a slice of them`,
		`main.go:58:2: Sift.Among is a []Shown: a query's field is a string, a number, a bool, a type that reads itself from text, a pointer to one, or a slice of them`,
		`main.go:62:19: plugin.Query is used here, and not as what plugin.Get or plugin.Delete is given: apigen reads an endpoint's query where the endpoint is declared, so write it there`,
		`main.go:67:2: GET /api/words's query is a string: a query is a struct with a name, with a field for each name it has`,
	}, "\n")
	if err.Error() != want {
		t.Errorf("what it said:\n%s\n\nnot:\n%s", err, want)
	}
}
