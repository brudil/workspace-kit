// Package protocol is the plugin protocol's messages, as Go types: what ws
// and a plugin send each other over the plugin's stdin and stdout. See
// PROTOCOL.md at the root of this repository.
package protocol

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Version is the newest version of the protocol this package speaks.
const Version = 1

// Versions are every version of the protocol this package speaks, oldest
// first. Within a version, changes only add; anything else is a new
// version, with types of its own.
var Versions = []int{1}

// Choose is the version a handshake settles on: the highest that both
// sides speak, or 0 when they have none in common.
func Choose(a, b []int) int {
	chosen := 0
	for _, v := range a {
		if v > chosen && slices.Contains(b, v) {
			chosen = v
		}
	}
	return chosen
}

// What ws sends a plugin.
const (
	MethodInitialize    = "initialize"     // request, always first
	MethodConfigChanged = "config/changed" // notification
	MethodCommandRun    = "command/run"    // request
	MethodShutdown      = "shutdown"       // request
	// MethodRefShow asks what one of the plugin's things is, by its name:
	// RefShowParams → RefShowResult. An addition to version 1: a plugin
	// from before it answers CodeMethodNotFound, and registers no refs.
	MethodRefShow = "ref/show" // request
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
	// Protocol is the newest version ws speaks.
	Protocol int `json:"protocol"`
	// Protocols are every version ws speaks. A ws from before versions
	// were chosen doesn't send it, and speaks only Protocol.
	Protocols []int      `json:"protocols,omitempty"`
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
	// Executable is ws's own program, for a plugin that runs ws's
	// commands. A ws from before it said doesn't.
	Executable string `json:"executable,omitempty"`
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

// ViewInfo is where a plugin serves its own HTTP (its app's files and
// what its app asks it): a unix socket to listen on, and the path ws
// serves it under.
type ViewInfo struct {
	Listen string `json:"listen"`
	Base   string `json:"base"`
}

// Offered are the versions ws speaks, from what it said. A ws that says
// nothing of it speaks 1.
func (in InitializeParams) Offered() []int {
	switch {
	case len(in.Protocols) > 0:
		return in.Protocols
	case in.Protocol > 0:
		return []int{in.Protocol}
	}
	return []int{1}
}

// InitializeResult is a plugin's answer: the version it chose, the
// versions it speaks, and what it adds.
type InitializeResult struct {
	// Protocol is the version chosen (Choose), which everything after
	// initialize is in. 0: ws and the plugin have none in common, and the
	// plugin registers nothing.
	Protocol int `json:"protocol"`
	// Protocols are every version the plugin speaks, so that ws can say
	// which side is behind when there's none in common.
	Protocols    []int        `json:"protocols,omitempty"`
	Registration Registration `json:"registration"`
}

// Registration is everything a plugin adds to ws. Every field is optional.
type Registration struct {
	Description   string      `json:"description,omitempty"`
	Views         []View      `json:"views,omitempty"`
	UI            *UI         `json:"ui,omitempty"`
	Commands      *Commands   `json:"commands,omitempty"`
	Agent         *Agent      `json:"agent,omitempty"`
	Syncs         []Sync      `json:"syncs,omitempty"`
	Refs          []Ref       `json:"refs,omitempty"`
	ConfigVersion int         `json:"config_version,omitempty"`
	Config        []ConfigKey `json:"config,omitempty"`
}

// CommandName is the word after ws that runs the plugin's commands: its
// own, or the plugin's name. "" if it adds none.
func (r Registration) CommandName(plugin string) string {
	if r.Commands == nil {
		return ""
	}
	if r.Commands.Name != "" {
		return r.Commands.Name
	}
	return plugin
}

// View is an entry in ws's sidebar, and where in the plugin's app it opens.
type View struct {
	Title string `json:"title"`
	Icon  string `json:"icon,omitempty"`
	Under string `json:"under,omitempty"`
	Path  string `json:"path"`
}

// UI is the plugin's app: what ws's page for the plugin loads. A plugin
// has one, whatever views it has, and serves no HTML of its own. Each is
// a path in the plugin's own HTTP, with whatever query it has
// ("/ui/app.js?v=3f9c2a").
type UI struct {
	// Script is the app's entry, loaded as a module. A plugin with views
	// has one.
	Script string `json:"script,omitempty"`
	// Style is its stylesheet, if it has one, loaded before the app.
	Style string `json:"style,omitempty"`
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

// Ref is a kind of thing the plugin has that words name (a Jira issue,
// RAM-123), and how one is named: what ws finds in what's written, links,
// and asks the plugin about (MethodRefShow). It's data, not code: ws finds
// names in its own program and in its web UI, from this alone.
type Ref struct {
	// Kind is the plugin's word for what it is (issue): lower-case
	// letters, digits and hyphens. ws asks about one by it.
	Kind string `json:"kind"`
	// Title is what one is called, for a person: Jira issue.
	Title string `json:"title,omitempty"`
	// Icon is one of ws's icon names.
	Icon string `json:"icon,omitempty"`
	// Prefixes are what a name starts with (RAM-, RAP-): letters, digits
	// and underscores, starting with a letter and ending in a hyphen. A
	// name is one of them and its Tail, as written: ram-12 isn't RAM-12.
	Prefixes []string `json:"prefixes"`
	// Tail is what follows the prefix: one of the Tails, TailNumber when
	// it's not said. A ws that doesn't know a tail reads none of that
	// kind's names.
	Tail string `json:"tail,omitempty"`
	// Address is where one is outside ws, with RefPlaceholder where its
	// name goes: https://acme.atlassian.net/browse/{ref}. ws links a name
	// to it in a document, and reads one pasted as the thing it names.
	Address string `json:"address,omitempty"`
	// Path is where one opens in the plugin's app, with RefPlaceholder
	// where its name goes: /issue/{ref}.
	Path string `json:"path,omitempty"`
}

// RefPlaceholder is where a thing's name goes in a Ref's Address and Path.
const RefPlaceholder = "{ref}"

// The tails of a name: what follows its prefix.
const (
	// TailNumber is a whole number that doesn't start with 0, of up to
	// nine digits: RAM-123.
	TailNumber = "number"
)

var (
	refKind   = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	refPrefix = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,30}-$`)
)

// Problem says what's wrong with a ref as it's registered, or "": ws
// leaves out one with a problem, and the framework won't declare it.
func (r Ref) Problem() string {
	switch {
	case !refKind.MatchString(r.Kind):
		return fmt.Sprintf("its kind %q must be lower-case letters, digits and hyphens, starting with a letter", r.Kind)
	case len(r.Prefixes) == 0:
		return "it has no prefixes: nothing would be named by it"
	}
	for _, p := range r.Prefixes {
		if !refPrefix.MatchString(p) {
			return fmt.Sprintf("its prefix %q must be letters, digits and underscores, starting with a letter and ending in a hyphen (RAM-)", p)
		}
	}
	if r.Address != "" {
		u, err := url.Parse(strings.Replace(r.Address, RefPlaceholder, "X-1", 1))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || strings.Count(r.Address, RefPlaceholder) != 1 {
			return fmt.Sprintf("its address %q must be an http or https address with %s once, where the name goes", r.Address, RefPlaceholder)
		}
		if strings.Contains(u.Host, "X-1") || strings.ContainsAny(r.Address, " \t\n") {
			return fmt.Sprintf("its address %q must have %s after its host", r.Address, RefPlaceholder)
		}
	}
	if r.Path != "" && (!strings.HasPrefix(r.Path, "/") || strings.HasPrefix(r.Path, "//") || strings.Count(r.Path, RefPlaceholder) != 1) {
		return fmt.Sprintf("its path %q must be a path in the plugin's app (/issue/%s) with %s once", r.Path, RefPlaceholder, RefPlaceholder)
	}
	return ""
}

// RefShowParams asks about one thing: its Ref's kind, and its name as it
// was written. Full: everything was asked for (ws show --full), so nothing
// is to be left out of the markdown.
type RefShowParams struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
	Full bool   `json:"full,omitempty"`
}

// RefShowResult is a thing as ws shows it: in a card under its name, and
// as ws show prints it for a person or an agent. One the plugin has
// nothing by that name for is answered CodeNotFound.
type RefShowResult struct {
	Title string `json:"title"`
	// State is where it stands, in the plugin's words: In Progress.
	State string `json:"state,omitempty"`
	// Fields are a few things worth saying of it in a line each: who has
	// it, how pressing it is.
	Fields []RefField `json:"fields,omitempty"`
	// Markdown is what it says, for ws show: its description, what's
	// been said on it. ws says where it came from before it: it's read
	// as information, not as instructions.
	Markdown string `json:"markdown,omitempty"`
}

// RefField is one thing said of a thing: Assignee, Dana.
type RefField struct {
	Label string `json:"label"`
	Value string `json:"value"`
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

// Secret says whether the key's value is a secret: kept apart from
// config's files, and always local.
func (k ConfigKey) Secret() bool { return k.Type == TypeSecret }

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

// ViewBadgeParams is the count beside a view's sidebar entry: the view
// registered with that path.
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

// What a plugin asks ws: requests, each answered. Each is an addition to
// version 1: a ws from before it answers CodeMethodNotFound.
const (
	MethodMissionList = "mission/list" // MissionListParams → MissionListResult
	MethodMissionShow = "mission/show" // MissionShowParams → MissionDetail
	MethodRepoList    = "repo/list"    // nothing → RepoListResult
)

// CodeNotFound is the answer for something asked for by name that isn't
// there: ws's for a mission with no such slug, a plugin's for a ref it has
// nothing by.
const CodeNotFound = -32001

// The states of a mission. One that's ended is landed, answered,
// handed-on or dropped; one that's archived has ended, and is put away.
const (
	MissionBriefing = "briefing" // its brief isn't confirmed yet
	MissionDraft    = "draft"    // confirmed, and nothing's been made
	MissionActive   = "active"
	MissionReview   = "review" // its pull requests are open
	MissionLanded   = "landed"
	MissionParked   = "parked"
	MissionAnswered = "answered"
	MissionHandedOn = "handed-on"
	MissionDropped  = "dropped"
	MissionArchived = "archived"
)

// Whose move a mission's headline says it is.
const (
	CourtAgent   = "agent"   // crew, a briefing agent or the orchestrator is on it
	CourtYou     = "you"     // it waits on the person
	CourtOutside = "outside" // it waits on GitHub: reviewers, checks, a merge
)

// MissionListParams says which missions: those in play, and with
// Archived, those archived too.
type MissionListParams struct {
	Archived bool `json:"archived,omitempty"`
}

// MissionListResult is the workspace's missions, those in play first.
type MissionListResult struct {
	Missions []Mission `json:"missions"`
}

// MissionShowParams names a mission by its slug.
type MissionShowParams struct {
	Slug string `json:"slug"`
}

// Mission is a mission as a list shows it.
type Mission struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	// State is one of the Mission states.
	State   string    `json:"state"`
	Created time.Time `json:"created"`
	// Updated is when anything of it last changed.
	Updated time.Time `json:"updated"`
	// Headline is where it stands now, and whose move it is. Nil for an
	// archived mission.
	Headline *Headline `json:"headline,omitempty"`
	// At is where it is on its route, in words.
	At       string           `json:"at,omitempty"`
	Capsules []MissionCapsule `json:"capsules"`
	// Path is its page in ws's web UI: what a plugin's app opens with
	// ws:open.
	Path string `json:"path"`
}

// Headline is a mission's one line of where it stands.
type Headline struct {
	// Court is one of the Courts: whose move it is.
	Court string `json:"court"`
	Text  string `json:"text"`
}

// MissionCapsule is a capsule a mission works in: a repo's branch, in a
// folder of its own while it exists, and its pull request once it has one.
type MissionCapsule struct {
	Repo   string `json:"repo"`
	Name   string `json:"name"`
	Branch string `json:"branch,omitempty"`
	// Path is its folder, while it exists.
	Path        string       `json:"path,omitempty"`
	PullRequest *PullRequest `json:"pull_request,omitempty"`
	// Landed: a capsule of a repo with no pull requests has landed its
	// commits on the default branch.
	Landed bool `json:"landed,omitempty"`
}

// PullRequest is a pull request on GitHub, as ws last heard of it.
type PullRequest struct {
	Number int    `json:"number"`
	Title  string `json:"title,omitempty"`
	URL    string `json:"url"`
	// State is open, merged or closed.
	State string `json:"state"`
}

// MissionDetail is a mission as its page shows it: as a list does, with
// its brief and its plan.
type MissionDetail struct {
	Mission
	Brief string `json:"brief"`
	Plan  string `json:"plan,omitempty"`
}

// RepoListResult is the workspace's repos, in ws's order.
type RepoListResult struct {
	Repos []Repo `json:"repos"`
}

// Repo is one of the workspace's repos, and its capsules.
type Repo struct {
	Name string `json:"name"`
	// Title is what ws calls it, where that isn't its name.
	Title   string   `json:"title"`
	Group   string   `json:"group,omitempty"`
	Aliases []string `json:"aliases,omitempty"`
	// Cloned: it's here, not only named in ws.toml.
	Cloned bool `json:"cloned"`
	// Path is its folder: its capsules are in it, and its default branch
	// is checked out in Ground.
	Path     string    `json:"path"`
	Capsules []Capsule `json:"capsules"`
}

// Capsule is a repo's branch, checked out in a folder of its own.
type Capsule struct {
	Name   string `json:"name"`
	Branch string `json:"branch,omitempty"`
	Path   string `json:"path"`
	// Ground: it's the repo's default branch, which work starts from.
	Ground bool `json:"ground,omitempty"`
	// Mission is the slug of the mission in play that works in it.
	Mission string `json:"mission,omitempty"`
}
