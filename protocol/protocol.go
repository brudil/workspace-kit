// Package protocol is the plugin protocol's messages, as Go types: what ws
// and a plugin send each other over the plugin's stdin and stdout. See
// PROTOCOL.md at the root of this repository.
package protocol

import "encoding/json"

// Version is the protocol's version, said by both sides in initialize.
const Version = 1

// What ws sends a plugin.
const (
	MethodInitialize    = "initialize"     // request, always first
	MethodConfigChanged = "config/changed" // notification
	MethodCommandRun    = "command/run"    // request
	MethodShutdown      = "shutdown"       // request
)

// What a plugin sends ws: notifications all.
const (
	MethodCommandOutput = "command/output"
	MethodLog           = "log"
	MethodViewChanged   = "view/changed"
	MethodViewBadge     = "view/badge"
	MethodSyncState     = "sync/state"
)

// MethodCancelRequest is how either side gives up on a request it made.
const MethodCancelRequest = "$/cancel_request"

// JSON-RPC's error codes, and the one for a request given up on.
const (
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternal       = -32603
	CodeCancelled      = -32800
)

// InitializeParams is what ws tells a plugin as it starts.
type InitializeParams struct {
	Protocol  int        `json:"protocol"`
	WS        WSInfo     `json:"ws"`
	Plugin    PluginInfo `json:"plugin"`
	Workspace Workspace  `json:"workspace"`
	// Config is what's set, secrets included, by key. A key that isn't
	// set is absent.
	Config map[string]json.RawMessage `json:"config"`
	View   ViewInfo                   `json:"view"`
}

// WSInfo is the ws on the other end.
type WSInfo struct {
	Version string `json:"version"`
}

// PluginInfo is the plugin as ws knows it: its plugin.toml, and its folders.
type PluginInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Dir     string `json:"dir"`
	Data    string `json:"data"`
}

// Workspace is the workspace the plugin is installed in.
type Workspace struct {
	Root string `json:"root"`
}

// ViewInfo is where a plugin serves its view: a unix socket to listen on,
// and the path ws serves it under.
type ViewInfo struct {
	Listen string `json:"listen"`
	Base   string `json:"base"`
}

// InitializeResult is a plugin's answer: what it speaks, and what it adds.
type InitializeResult struct {
	Protocol     int          `json:"protocol"`
	Registration Registration `json:"registration"`
}

// Registration is everything a plugin adds to ws. Every field is optional.
type Registration struct {
	Description   string      `json:"description,omitempty"`
	Views         []View      `json:"views,omitempty"`
	Commands      *Commands   `json:"commands,omitempty"`
	Agent         *Agent      `json:"agent,omitempty"`
	Syncs         []Sync      `json:"syncs,omitempty"`
	ConfigVersion int         `json:"config_version,omitempty"`
	Config        []ConfigKey `json:"config,omitempty"`
}

// View is an entry in ws's sidebar, and where in the plugin's view it opens.
type View struct {
	Title string `json:"title"`
	Icon  string `json:"icon,omitempty"`
	Under string `json:"under,omitempty"`
	Path  string `json:"path"`
}

// Commands is a plugin's command: the word after ws, and what follows it.
type Commands struct {
	Name        string       `json:"name,omitempty"`
	Short       string       `json:"short,omitempty"`
	Subcommands []Subcommand `json:"subcommands,omitempty"`
}

// Subcommand is one thing the command does.
type Subcommand struct {
	Name  string `json:"name"`
	Short string `json:"short,omitempty"`
	Args  string `json:"args,omitempty"`
}

// Agent is what agents are told of the plugin. No roles means every role.
type Agent struct {
	Roles []string `json:"roles,omitempty"`
	Guide string   `json:"guide,omitempty"`
}

// Sync is a sync the plugin runs, for show. Every is in seconds.
type Sync struct {
	Name  string `json:"name"`
	Every int    `json:"every"`
}

// The types of a config key.
const (
	TypeString = "string"
	TypeURL    = "url"
	TypeBool   = "bool"
	TypeNumber = "number"
	TypeEnum   = "enum"
	TypeList   = "list"
	TypeSecret = "secret"
)

// Where a config key's value is kept.
const (
	ScopeShared = "shared"
	ScopeLocal  = "local"
)

// ConfigKey is one key of a plugin's config.
type ConfigKey struct {
	Key      string   `json:"key"`
	Title    string   `json:"title,omitempty"`
	Type     string   `json:"type"`
	Required bool     `json:"required,omitempty"`
	Default  any      `json:"default,omitempty"`
	Help     string   `json:"help,omitempty"`
	Example  string   `json:"example,omitempty"`
	Options  []string `json:"options,omitempty"`
	Scope    string   `json:"scope,omitempty"`
}

// ConfigChangedParams is the whole config, after a change.
type ConfigChangedParams struct {
	Config map[string]json.RawMessage `json:"config"`
}

// CommandRunParams is a command someone ran: everything after the
// command's name, where it was run from, and what's on the other end.
type CommandRunParams struct {
	// ID is ws's name for this run: its output carries it.
	ID       string   `json:"id"`
	Args     []string `json:"args"`
	Dir      string   `json:"dir"`
	Terminal Terminal `json:"terminal"`
}

// Terminal is what a command's output is going to.
type Terminal struct {
	TTY   bool `json:"tty"`
	Width int  `json:"width"`
	Color bool `json:"color"`
}

// CommandRunResult ends a command.
type CommandRunResult struct {
	Exit int `json:"exit"`
}

// The streams of a command's output.
const (
	Stdout = "stdout"
	Stderr = "stderr"
)

// CommandOutputParams is a piece of a running command's output. ID is the
// id of the command/run it belongs to.
type CommandOutputParams struct {
	ID     string `json:"id"`
	Stream string `json:"stream"`
	Data   string `json:"data"`
}

// LogParams is a line for ws to show: level is debug, info, warn or error.
type LogParams struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}

// ViewBadgeParams is the count beside a view's sidebar entry.
type ViewBadgeParams struct {
	Path  string `json:"path"`
	Count int    `json:"count"`
}

// SyncStateParams says a sync finished. At is RFC 3339.
type SyncStateParams struct {
	Name   string `json:"name"`
	At     string `json:"at"`
	TookMS int64  `json:"took_ms"`
	Error  string `json:"error"`
}
