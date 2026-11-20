// Command apigen writes a plugin's app the TypeScript of what the plugin's
// endpoints take and answer (plugin.Get, plugin.Post…): the types, and
// Api, which says which endpoint has which. The app asks through
// @withworkspace/ui-lib's endpoints, given Api, so it can't ask for what
// isn't there or read what isn't sent.
//
// It's run in the plugin's folder, as a tool of the plugin's module:
//
//	go get -tool github.com/withworkspace/plugin/cmd/apigen   # once
//	go tool apigen          # write ui/src/api.gen.ts
//	go tool apigen -check   # fail if it isn't what the Go says
//
// As a tool of the module it's the framework's own version of itself, so
// it reads the plugin.Get the plugin is built with.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/withworkspace/plugin/internal/apigen"
)

func main() {
	out := flag.String("o", filepath.FromSlash("ui/src/api.gen.ts"), "the file to write")
	check := flag.Bool("check", false, "fail if the file isn't what the Go says, and write nothing")
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: go tool apigen [-check] [-o file]\n\nWrites the TypeScript of what the plugin's endpoints take and answer.\nRun it in the plugin's folder.")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}
	// What's wrong with the plugin's Go is said once, by apigen: guts
	// logs the same as it loads the packages again.
	slog.SetDefault(slog.New(slog.DiscardHandler))
	ts, err := apigen.Generate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "apigen: %v\n", err)
		os.Exit(1)
	}
	have, err := os.ReadFile(*out)
	if *check {
		if err != nil || !bytes.Equal(have, []byte(ts)) {
			fmt.Fprintf(os.Stderr, "%s isn't what the plugin's endpoints say: run `go tool apigen`, and commit it\n", filepath.ToSlash(*out))
			os.Exit(1)
		}
		return
	}
	if err == nil && bytes.Equal(have, []byte(ts)) {
		return // as it is: nothing for a watcher to see change
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "apigen: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, []byte(ts), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "apigen: %v\n", err)
		os.Exit(1)
	}
}
