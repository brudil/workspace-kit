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
	// MethodActionRun runs one of the plugin's actions, its own or on one
	// of its things: ActionRunParams → ActionRunResult. An addition to
	// version 1: a plugin that registers actions answers it.
	MethodActionRun = "action/run" // request
	// MethodViewLooking is an addition to version 1: a ws from before it
	// never says who's looking, and a plugin from before it drops it.
	MethodViewLooking = "view/looking" // notification
	// MethodProposalDraft is a conversation's agent proposing one of the
	// plugin's Proposes: ProposalDraftParams → ProposalDraftResult. With
	// no input, it's asking how one's written. An addition to version 1:
	// a plugin that registers Proposes answers it.
	MethodProposalDraft = "proposal/draft" // request
	// MethodProposalAccept is the user accepting what was drafted, all of
	// it or some of its items: ProposalAcceptParams → ProposalAcceptResult.
	MethodProposalAccept = "proposal/accept" // request
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
	Description string    `json:"description,omitempty"`
	Views       []View    `json:"views,omitempty"`
	UI          *UI       `json:"ui,omitempty"`
	Commands    *Commands `json:"commands,omitempty"`
	Agent       *Agent    `json:"agent,omitempty"`
	Syncs       []Sync    `json:"syncs,omitempty"`
	Refs        []Ref     `json:"refs,omitempty"`
	// Actions are things the plugin can do, offered among what there is
	// to do in Find. What can be done to one of its things is the thing's
	// kind's (Ref.Actions).
	Actions []Action `json:"actions,omitempty"`
	// Proposes are what a conversation's agent may propose of the
	// plugin's, for the user to accept: issues to create, say. The plugin
	// says how one's written, checks what's written, and does it once
	// it's accepted; ws keeps it, draws it and asks the user.
	Proposes      []Proposes  `json:"proposes,omitempty"`
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
	// Conversations is a command a conversation's agent may run itself,
	// not only propose: what it changes is changed as the agent runs it.
	// An addition to version 1: a ws from before it runs none of a
	// plugin's commands in a conversation.
	Conversations bool `json:"conversations,omitempty"`
}

// Agent is what agents are told of the plugin. No roles means every role.
type Agent struct {
	Roles []string `json:"roles,omitempty"`
	Guide string   `json:"guide,omitempty"`
}

// Sync is a sync the plugin runs, for show. Every is in seconds: 0 is a
// sync with no interval of its own. Open, in seconds too, is its interval
// while the plugin's app is on screen, where that's another.
type Sync struct {
	Name  string `json:"name"`
	Every int    `json:"every"`
	Open  int    `json:"open,omitempty"`
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
	// Fields are what one of its things is made of: what the plugin
	// tells ws of a thing (thing/put, ref/show) is a value for each. One
	// of them, of type FieldTitle, is what a thing's called: a kind with
	// none has names, and nothing ws keeps or draws.
	Fields []Field `json:"fields,omitempty"`
	// Search is what a thing is found by, in order: which fields, and
	// how much of each. None: every field, as far as ws keeps of it.
	Search []Searched `json:"search,omitempty"`
	// Views are how one is drawn, by where: ViewCard, under its name,
	// and ViewPane, beside the list in Find. Each is blocks, in order.
	// With none for somewhere, ws draws one from the fields' types. A
	// view for somewhere ws doesn't know is ignored.
	Views map[string][]Block `json:"views,omitempty"`
	// Actions are what can be done to one of its things: offered in Find
	// for the thing the cursor's on, and run with its name.
	Actions []Action `json:"actions,omitempty"`
	// Remote is where one lives beyond this machine, for a kind whose
	// things can: a folder of a repo, for everyone with the repo. An
	// addition to version 1: a ws from before it leaves it out.
	Remote *Remote `json:"remote,omitempty"`
}

// Remote is where a kind's things live beyond this machine: a folder of
// a repo each (its package), which holds the thing's own file (its main
// file), a ManifestName that says what it is, and what the plugin makes
// from the main file (a picture of it). ws finds a package in one of the
// workspace's spaces by its manifest, names the thing by the folder as it
// names any folder (records/sketches/release-flow/), calls it what the
// manifest says, and asks the plugin what its main file is found by
// (MethodRemoteRead). The plugin mints no name for one, and needn't keep
// one: ws has them, and says which (MethodRemoteList, MethodRemoteOpen).
type Remote struct {
	// Path is the folder a new one goes in, in its repo, with RemoteSlug
	// where the thing's slug goes: sketches/{slug}/. One that's there is
	// found wherever its folder is.
	Path string `json:"path"`
	// Main is the main file's name in that folder, with RemoteSlug where
	// the thing's slug goes: {slug}.excalidraw.
	Main string `json:"main"`
	// Reads is whether the plugin says what a main file is found by
	// (MethodRemoteRead). Without it, one is found by its title alone.
	Reads bool `json:"reads,omitempty"`
}

// ManifestName is the file in a package that says what it is, in TOML:
// its kind (as the plugin registered it), its title, its main file's name
// and its picture's, if it has one. It's ws's own file: ws reads it, and
// writes it.
//
//	kind    = "sketch"
//	title   = "Release flow"
//	main    = "release-flow.excalidraw"
//	picture = "release-flow.svg"
const ManifestName = "thing.toml"

// RemoteSlug is where a thing's slug goes in a Remote's Path and Main.
const RemoteSlug = "{slug}"

// Field is one of the things a kind's things are made of: its name, in
// lower-case letters, digits and underscores, what it is, and what it's
// called where it's drawn with a label.
type Field struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Label string `json:"label,omitempty"`
	// Link is how a FieldRef or a FieldRefs points at what it names, in
	// ws's graph: one of the Links. None, or one ws doesn't know, is
	// LinkMentions.
	Link string `json:"link,omitempty"`
}

// How a field that names other things points at them: ws's graph has a
// short, closed list, so that whatever reads a link can say why it's
// worth following.
const (
	LinkMentions = "mentions" // it names it
	LinkIn       = "in"       // it's part of it: a child, of its parent
	LinkAbout    = "about"    // it's about it
	LinkFrom     = "from"     // it came out of it
	LinkOf       = "of"       // it's one of its: a comment, a review
)

// FieldKey is the field every thing has without its kind saying so: its
// name (RAM-123). A view's block and what's searched may name it.
const FieldKey = "key"

// The types of a field. One a ws doesn't know is drawn as text.
const (
	FieldTitle    = "title"    // what the thing's called: a line. A kind has one.
	FieldState    = "state"    // where it stands, in the plugin's words: at most one
	FieldText     = "text"     // words: a name, a number, a few of them
	FieldTime     = "time"     // a moment, RFC 3339
	FieldMarkdown = "markdown" // what it says, at length
	// A field that names other things: one name, or several, separated by
	// spaces, commas or lines. Each that's a name ws reads (another of
	// the plugin's things, any plugin's, a pull request) is a link in
	// ws's graph, from this thing to it (Field.Link), and is drawn as
	// what it names.
	FieldRef  = "ref"
	FieldRefs = "refs"
)

// Searched is a field a thing is found by, and how much of it: Max bytes,
// or all ws keeps of it (0).
type Searched struct {
	Field string `json:"field"`
	Max   int    `json:"max,omitempty"`
}

// Where a kind's things are drawn from a view of the plugin's.
const (
	ViewCard = "card" // under a thing's name, on hover
	ViewPane = "pane" // beside the list in Find, for the row the cursor's on
)

// Block is one piece of a view: what it is, and the field it shows. A
// block whose field a thing has nothing for isn't drawn, and neither is
// one of a kind a ws doesn't know.
type Block struct {
	Block string `json:"block"`
	Field string `json:"field"`
	// Label is what a BlockField says before its value, where that isn't
	// the field's own.
	Label string `json:"label,omitempty"`
	// Lines is how many lines of a BlockMarkdown are drawn: 0 for all.
	Lines int `json:"lines,omitempty"`
}

// The kinds of block.
const (
	BlockTitle    = "title"    // the line it's known by
	BlockSubtitle = "subtitle" // a quiet line under it
	BlockBadge    = "badge"    // a word or two, set apart: where it stands
	BlockField    = "field"    // a label and a value
	BlockMarkdown = "markdown" // markdown, drawn as ws draws it
)

// Named is the field of that name, if the kind has it.
func (r Ref) Named(name string) (Field, bool) {
	for _, f := range r.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return Field{}, false
}

// Typed is the name of the kind's first field of a type: "" for none.
func (r Ref) Typed(fieldType string) string {
	for _, f := range r.Fields {
		if f.Type == fieldType {
			return f.Name
		}
	}
	return ""
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
	refField  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
)

var tailNumber = regexp.MustCompile(`^[1-9][0-9]{0,8}$`)

// Names says whether name is one of this kind's: one of its prefixes and
// its tail, whole and as written. A kind whose tail this release doesn't
// know names nothing.
func (r Ref) Names(name string) bool {
	if r.Tail != "" && r.Tail != TailNumber {
		return false
	}
	for _, p := range r.Prefixes {
		if tail, ok := strings.CutPrefix(name, p); ok && tailNumber.MatchString(tail) {
			return true
		}
	}
	return false
}

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
	// What its things are made of, found by and drawn from.
	has := map[string]bool{FieldKey: true}
	titles, states := 0, 0
	for _, f := range r.Fields {
		switch {
		case f.Name == FieldKey:
			return fmt.Sprintf("its field %q is every thing's own: its name", FieldKey)
		case !refField.MatchString(f.Name):
			return fmt.Sprintf("its field %q must be lower-case letters, digits and underscores, starting with a letter", f.Name)
		case has[f.Name]:
			return fmt.Sprintf("its field %q is there twice", f.Name)
		}
		has[f.Name] = true
		switch f.Type {
		case FieldTitle:
			titles++
		case FieldState:
			states++
		case "":
			return fmt.Sprintf("its field %q has no type", f.Name)
		}
	}
	switch {
	case len(r.Fields) > 0 && titles != 1:
		return fmt.Sprintf("it has %d fields of type %s: a thing has one thing it's called", titles, FieldTitle)
	case states > 1:
		return fmt.Sprintf("it has %d fields of type %s: a thing stands in one place", states, FieldState)
	}
	for _, s := range r.Search {
		if !has[s.Field] || s.Max < 0 {
			return fmt.Sprintf("it's found by %q, which isn't one of its fields", s.Field)
		}
	}
	for where, blocks := range r.Views {
		for _, b := range blocks {
			if !has[b.Field] {
				return fmt.Sprintf("its view %s shows %q, which isn't one of its fields", where, b.Field)
			}
		}
	}
	if r.Remote != nil {
		if problem := r.Remote.Problem(); problem != "" {
			return problem
		}
		if titles != 1 {
			return "it has a remote and no title field: one in a repo is called what its " + ManifestName + " says"
		}
	}
	return ActionsProblem(r.Actions)
}

var (
	remotePath = regexp.MustCompile(`^([A-Za-z0-9._-]+/)*[A-Za-z0-9._-]*\{slug\}[A-Za-z0-9._-]*/$`)
	remoteMain = regexp.MustCompile(`^[A-Za-z0-9._-]*\{slug\}[A-Za-z0-9._-]*\.[A-Za-z0-9]+$`)
)

// Problem says what's wrong with a remote as it's registered, or "".
func (r Remote) Problem() string {
	switch {
	case !remotePath.MatchString(r.Path) || strings.Contains(r.Path, ".."):
		return fmt.Sprintf("its remote's path %q must be a folder in a repo, ending in /, with %s in its last part (sketches/%s/)", r.Path, RemoteSlug, RemoteSlug)
	case !remoteMain.MatchString(r.Main) || strings.Contains(r.Main, ".."):
		return fmt.Sprintf("its remote's main file %q must be a file's name with %s in it and a type after (%s.excalidraw)", r.Main, RemoteSlug, RemoteSlug)
	}
	// A main file that some slug would name as the manifest is.
	before, after, _ := strings.Cut(strings.ToLower(r.Main), RemoteSlug)
	if len(before)+len(after) < len(ManifestName) && strings.HasPrefix(ManifestName, before) && strings.HasSuffix(ManifestName, after) {
		return fmt.Sprintf("its remote's main file %q could be %s, ws's own file in the folder", r.Main, ManifestName)
	}
	return ""
}

// Action is something a plugin can do when it's asked from Find: its id,
// in lower-case letters, digits and hyphens, and what it's called there.
type Action struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Icon is one of ws's icon names. Hint says more, after the title.
	Icon string `json:"icon,omitempty"`
	Hint string `json:"hint,omitempty"`
}

var actionID = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

// ActionsProblem says what's wrong with a list of actions as registered,
// or "".
func ActionsProblem(actions []Action) string {
	seen := map[string]bool{}
	for _, a := range actions {
		switch {
		case !actionID.MatchString(a.ID):
			return fmt.Sprintf("its action %q must be lower-case letters, digits and hyphens, starting with a letter", a.ID)
		case seen[a.ID]:
			return fmt.Sprintf("its action %q is there twice", a.ID)
		case strings.TrimSpace(a.Title) == "":
			return fmt.Sprintf("its action %q has no title: it's what Find calls it", a.ID)
		}
		seen[a.ID] = true
	}
	return ""
}

// ActionRunParams runs an action: the plugin's own, by its id, or one of
// a kind's on one thing, by the kind and the thing's name. Answers are
// what's been answered so far to what the action asked, in order: an
// action that asks is run again with one more, from the start.
type ActionRunParams struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind,omitempty"`
	Ref     string   `json:"ref,omitempty"`
	Answers []string `json:"answers"`
}

// ActionRunResult is what came of it. With Ask, nothing's been done yet:
// the action wants one thing answered, and is run again with the answer.
// Otherwise it's done: Say is a line to tell whoever asked, and Open a
// path in the plugin's app to go to.
type ActionRunResult struct {
	Ask  *Ask   `json:"ask,omitempty"`
	Say  string `json:"say,omitempty"`
	Open string `json:"open,omitempty"`
}

// Ask is one thing an action wants answered before it does anything.
type Ask struct {
	Title string `json:"title"`
	// Type is AskPick, one of Options, or AskText, a line typed.
	Type        string   `json:"type"`
	Options     []Option `json:"options,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
}

// The types of an ask.
const (
	AskPick = "pick"
	AskText = "text"
)

// Option is one thing to pick: what's answered (Value), and what's shown.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
	Hint  string `json:"hint,omitempty"`
}

// RefShowParams asks about one thing: its Ref's kind, and its name as it
// was written. Full: everything was asked for (ws show --full), so nothing
// is to be left out of the markdown.
type RefShowParams struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
	Full bool   `json:"full,omitempty"`
}

// RefShowResult is a thing as the plugin has it now: a value for each of
// its kind's fields, as a Thing has them. ws shows it as it shows one
// it's been told of: by the kind's views in the web UI, and by its
// fields' types in ws show, where what's markdown is said to be from
// outside the workspace. One the plugin has nothing by that name for is
// answered CodeNotFound.
type RefShowResult struct {
	Changed time.Time         `json:"changed,omitzero"`
	Fields  map[string]string `json:"fields"`
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
	// TypeTable is named rows, each holding the key's Fields. Its value
	// is its rows, in the order they were written ([]ConfigRow). A ws from
	// before tables gives it as the file has it, an object of objects by
	// row name, which has no order.
	TypeTable = "table"
	// TypeMap is text to text: an object of strings.
	TypeMap = "map"
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
	// Fields are what each row of a table holds: keys of any type but a
	// secret. One may be a table itself, whose own fields may not be.
	// Row is what a row's name is called, in words ("Query", "Space
	// key"), for whoever's asked for one.
	Fields []ConfigKey `json:"fields,omitempty"`
	Row    string      `json:"row,omitempty"`
}

// ConfigRow is one row of a table as ws sends it: its name, and its
// fields' values. A field that's a table is rows in turn.
type ConfigRow struct {
	Name   string                     `json:"name"`
	Values map[string]json.RawMessage `json:"values"`
}

// Secret says whether the key's value is a secret: kept apart from
// config's files, and always local.
func (k ConfigKey) Secret() bool { return k.Type == TypeSecret }

// ConfigChangedParams is the whole config, after a change.
type ConfigChangedParams struct {
	Config map[string]json.RawMessage `json:"config"`
}

// ViewLookingParams is who's looking at what the plugin shows in ws's web
// UI. ws says it when it changes, and asks nothing by it: what a plugin
// fetches, and when, is the plugin's to decide.
type ViewLookingParams struct {
	// App: the plugin's app is on screen, in a page of ws's that's showing.
	App bool `json:"app"`
	// WS: a page of ws's web UI is showing, whichever it is, so the
	// plugin's sidebar entries and their badges are on screen. True
	// whenever App is.
	WS bool `json:"ws"`
}

// CommandRunParams is a command someone ran: everything after the
// command's name, where it was run from, and what's on the other end.
type CommandRunParams struct {
	// ID is ws's name for this run: its output carries it.
	ID       string   `json:"id"`
	Args     []string `json:"args"`
	Dir      string   `json:"dir"`
	Terminal Terminal `json:"terminal"`
	// Input is what's on the command's standard input: InputPiped or
	// InputTerminal, read with command/read. An addition to version 1: a
	// ws from before it says nothing, and gives the command nothing to
	// read.
	Input string `json:"input,omitempty"`
}

// What's on a command's standard input.
const (
	InputPiped    = "piped"    // a pipe or a file: ws … < ops.json
	InputTerminal = "terminal" // the person, typing
)

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

// MethodCommandRead is a running command reading its standard input:
// CommandReadParams → CommandReadResult. A request from the plugin, and
// an addition to version 1: a ws that sends a command/run its Input
// answers it.
const MethodCommandRead = "command/read"

// ReadAtOnce is the most one command/read gives: it gives what's there,
// up to Max or this, whichever is less.
const ReadAtOnce = 64 << 10

// CommandReadParams asks for what's next on a command's standard input.
// ID is the command/run's.
type CommandReadParams struct {
	ID  string `json:"id"`
	Max int    `json:"max"`
}

// CommandReadResult is what was read: some bytes, at least one unless
// EOF, which says there's no more. Data is base64 in JSON, so it's
// whatever was piped, cut wherever.
type CommandReadResult struct {
	Data []byte `json:"data,omitempty"`
	EOF  bool   `json:"eof,omitempty"`
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
	MethodShow        = "show"         // ShowParams → Shown
)

// What a plugin has ws do, for the person who's using it: a request,
// answered, and an addition to version 1.
const (
	MethodConversationStart = "conversation/start" // ConversationStartParams → ConversationStarted
)

// What a plugin tells ws of its things, so ws has them without asking:
// requests, each answered, and each an addition to version 1. ws keeps
// what it's told under the plugin, and forgets it when the plugin's
// removed.
const (
	MethodThingPut    = "thing/put"    // ThingPutParams → ThingPutResult
	MethodThingRemove = "thing/remove" // ThingRemoveParams → nothing
	MethodThingList   = "thing/list"   // ThingListParams → ThingListResult
)

// ThingsAtOnce is how many things one thing/put may hold: more is refused
// whole (CodeInvalidParams), so a plugin with more sends them in turn.
const ThingsAtOnce = 500

// Thing is one of the plugin's things, as ws keeps it: found by what its
// kind says it's found by (ws find, the web UI's Find) and drawn from its
// kind's views, without asking the plugin.
type Thing struct {
	// Kind is the Ref it's named by, and Ref its name: one of that
	// kind's, as the plugin registered them. ws keeps no thing it
	// couldn't name.
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
	// Changed is when it last changed, where the plugin knows: of two
	// found alike, the newer is first.
	Changed time.Time `json:"changed,omitzero"`
	// Fields are its values, by its kind's fields' names, each a string
	// (a time as RFC 3339). One its kind hasn't is dropped, and ws keeps
	// the start of a long one. A thing with nothing for its kind's title
	// isn't kept.
	Fields map[string]string `json:"fields"`
}

// ThingPutParams are things to keep: each added, or put in place of what
// ws had by that name.
type ThingPutParams struct {
	Things []Thing `json:"things"`
}

// ThingPutResult says which weren't kept, and why. The rest were.
type ThingPutResult struct {
	Refused []ThingRefused `json:"refused,omitempty"`
}

// ThingRefused is a thing ws didn't keep: its name isn't one of that
// kind's, it has nothing for its kind's title, or ws has something of its
// own by that name.
type ThingRefused struct {
	Kind string `json:"kind"`
	Ref  string `json:"ref"`
	Why  string `json:"why"`
}

// ThingRemoveParams are things of a kind to forget, by name. One ws
// doesn't have is nothing to forget.
type ThingRemoveParams struct {
	Kind string   `json:"kind"`
	Refs []string `json:"refs"`
}

// ThingListParams asks which things of a kind ws has from this plugin.
type ThingListParams struct {
	Kind string `json:"kind"`
}

// ThingListResult is their names, in order.
type ThingListResult struct {
	Refs []string `json:"refs"`
}

// A kind's things that live in a repo (Remote). ws finds them, so it asks
// the plugin what one's file is found by (a request to the plugin), and
// the plugin asks ws which there are and for one's file (requests to ws).
// Each is an addition to version 1.
const (
	// MethodRemoteRead is ws asking what a main file is found by:
	// RemoteReadParams → RemoteReadResult. Asked once for each version of
	// a file, of a kind whose Remote Reads.
	MethodRemoteRead = "remote/read"
	// MethodRemoteList is a plugin asking which things of a kind are in
	// the workspace's repos: RemoteListParams → RemoteListResult.
	MethodRemoteList = "remote/list"
	// MethodRemoteOpen is a plugin asking for one's main file:
	// RemoteOpenParams → RemoteOpened.
	MethodRemoteOpen = "remote/open"
)

// RemoteReadMax is the most of a main file ws sends to be read: one
// that's larger is found by its title alone.
const RemoteReadMax = 32 << 20

// RemoteReadParams is a main file to read: the thing's kind and name (its
// folder, as ws names one), what its manifest calls it, and the file.
type RemoteReadParams struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Title string `json:"title,omitempty"`
	// File is the main file's name, and Content what's in it.
	File    string `json:"file"`
	Content []byte `json:"content"`
}

// RemoteReadResult is what the file says: a value for each of the kind's
// fields that it has one for, as a Thing has them. Its title is its
// manifest's: one given here is dropped.
type RemoteReadResult struct {
	Fields map[string]string `json:"fields"`
}

// RemoteListParams asks which things of a kind are in the workspace's
// repos.
type RemoteListParams struct {
	Kind string `json:"kind"`
}

// RemoteListResult is those things, in order of name.
type RemoteListResult struct {
	Things []RemoteThing `json:"things"`
}

// RemoteThing is a thing that lives in a repo, as ws found it.
type RemoteThing struct {
	Kind string `json:"kind"`
	// Name is its name: its folder, as ws names one
	// (records/sketches/release-flow/).
	Name string `json:"name"`
	// Repo is the repo it's in, and Path its folder there, ending in /.
	Repo string `json:"repo"`
	Path string `json:"path"`
	// Title is what its manifest calls it.
	Title string `json:"title"`
	// Main is its main file's name, and Picture its picture's, if it has
	// one: both in its folder.
	Main    string `json:"main"`
	Picture string `json:"picture,omitempty"`
	// Version is its main file's content's id: the same for the same
	// content, and another when it changes.
	Version string `json:"version"`
	// Changed is when it last changed in its repo, and ChangedBy who by,
	// where ws knows.
	Changed   time.Time `json:"changed,omitzero"`
	ChangedBy string    `json:"changed_by,omitempty"`
}

// RemoteOpenParams asks for one thing's main file, by its kind and name.
type RemoteOpenParams struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}

// RemoteOpened is the thing, and its main file as it is on its repo's
// default branch. One ws has nothing by that name for is CodeNotFound.
type RemoteOpened struct {
	RemoteThing
	Content []byte `json:"content"`
}

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
	// About is what it's about, by name: one of a plugin's things
	// (RAM-123), a pull request, anything ws names. A plugin finds the
	// missions about one of its things by it.
	About []string `json:"about,omitempty"`
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

// ShowParams names one of the workspace's things by its ref, as ws show
// does: one of a plugin's things (RAM-123), a pull request (api#12), a
// document (records:docs/plan.md), one of the workspace's conversations,
// pages or artifacts (w4, p2, a3), or, with Mission, one of a mission's
// own (t3, d1, s2, c1, r1.c2). A mission itself is its slug, as Ref or
// as Mission with no Ref. Full is everything a view would otherwise cut
// short.
type ShowParams struct {
	Ref     string `json:"ref,omitempty"`
	Mission string `json:"mission,omitempty"`
	Full    bool   `json:"full,omitempty"`
}

// Shown is one of the workspace's things as ws show shows it: what it
// is, what it links to and what points at it.
type Shown struct {
	// Ref and Mission are what was asked for.
	Ref     string `json:"ref,omitempty"`
	Mission string `json:"mission,omitempty"`
	// Head is what it is, in a line, and Body the essentials of it: lines
	// and paragraphs of plain text, as ws show prints them.
	Head string   `json:"head"`
	Body []string `json:"body"`
	// Links are what it links to. Each is asked for in turn with its Ref
	// and this view's Mission.
	Links []ShownLink `json:"links"`
	// Said is what points at it from elsewhere in the workspace, what
	// still stands first, then latest first.
	Said []Said `json:"said"`
	// Path is its page in ws's web UI, where it has one.
	Path string `json:"path,omitempty"`
}

// ShownLink is something a view links to: its ref, and what it is to the
// view, in words.
type ShownLink struct {
	Ref  string `json:"ref"`
	What string `json:"what"`
}

// How one thing points at another.
const (
	SaidAbout    = "about"    // a mission, a thread or a talk → what it's about
	SaidOn       = "on"       // a comment or a finding → a file, at lines
	SaidBy       = "by"       // a document, a review, a decision → who made it
	SaidFor      = "for"      // a document, crew → the stage it's for
	SaidOf       = "of"       // a review → what's reviewed
	SaidIn       = "in"       // a decision → its thread; a cell → its page
	SaidFrom     = "from"     // a mission, a document → what it came from
	SaidMentions = "mentions" // text → what it names
	SaidLands    = "lands"    // a capsule → its pull request; a pull request → its mission
)

// Said is something that points at what a view shows.
type Said struct {
	// Ref is its ref where it's read: in Mission, or under PullRequest
	// (api#12), or, with neither, the workspace's. A mission's own Ref is
	// its slug, with no Mission. It's asked for in turn with its Ref and
	// Mission as they are, and one under a pull request by the pull
	// request.
	Ref         string `json:"ref"`
	Mission     string `json:"mission,omitempty"`
	PullRequest string `json:"pull_request,omitempty"`
	// Kind is what it is: mission, brief, handoff, file, decision,
	// comment, review, talk, thread-ask, thread-change, thread-note,
	// thing, pull-request, page, artifact, document, and whatever ws
	// adds.
	Kind  string `json:"kind"`
	Title string `json:"title"`
	// State is where it stands, in its kind's words, and Standing whether
	// that's still in play: open, current, a draft.
	State    string `json:"state,omitempty"`
	Standing bool   `json:"standing"`
	// How is how it points: one of the Said… constants, or whatever ws
	// adds. Lines is where in a file, as they were when it was said (17,
	// 17-20). At is when it was said, where ws has when: zero where it
	// hasn't.
	How   string    `json:"how"`
	Lines string    `json:"lines,omitempty"`
	At    time.Time `json:"at,omitzero"`
	// Path is its page in ws's web UI, where it has one.
	Path string `json:"path,omitempty"`
}

// ConversationStartParams is what a conversation is started with: what
// the person says first, as the plugin words it for them. What it names
// as ws names things (RAM-123) is read as that thing, so the
// conversation's agent can read it, and the thing says the conversation
// names it.
type ConversationStartParams struct {
	Message string `json:"message"`
}

// ConversationStarted is a conversation ws has started: one of the
// workspace's (w4), whose agent is answering, and its page in ws's web
// UI.
type ConversationStarted struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// Proposes is something a conversation's agent may propose of the
// plugin's: its id (the agent names it plugin:id), what it's called on
// the card that asks the user, and When, a line or two for the agent's
// first message on when to propose it. How one's written is the plugin's
// to say when it's asked (ProposalDraftParams with no input), as it is
// then.
type Proposes struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Icon  string `json:"icon,omitempty"`
	When  string `json:"when,omitempty"`
}

// ProposesProblem says what's wrong with a list of proposes as
// registered, or "".
func ProposesProblem(list []Proposes) string {
	seen := map[string]bool{}
	for _, p := range list {
		switch {
		case !actionID.MatchString(p.ID):
			return fmt.Sprintf("what it proposes, %q, must be lower-case letters, digits and hyphens, starting with a letter", p.ID)
		case seen[p.ID]:
			return fmt.Sprintf("it proposes %q twice", p.ID)
		case strings.TrimSpace(p.Title) == "":
			return fmt.Sprintf("what it proposes, %q, has no title: it's what the card calls it", p.ID)
		}
		seen[p.ID] = true
	}
	return ""
}

// ProposalDraftParams is a conversation's agent proposing something of
// the plugin's: which (ID), and what it wrote (Input, JSON of the
// plugin's own shape). No Input: it's asking how one's written. Where:
// the conversation it's proposed in, and its mission's slug, if it's a
// mission's.
type ProposalDraftParams struct {
	ID           string          `json:"id"`
	Input        json.RawMessage `json:"input,omitempty"`
	Conversation string          `json:"conversation"`
	Mission      string          `json:"mission,omitempty"`
}

// ProposalDraftResult answers a draft with one of: Guide, how one's
// written (asked with no input); Refused, what's wrong with what was
// written and what to write instead, for the agent; or Proposal, what
// the user's asked to accept.
type ProposalDraftResult struct {
	Guide    string    `json:"guide,omitempty"`
	Refused  string    `json:"refused,omitempty"`
	Proposal *Proposal `json:"proposal,omitempty"`
}

// Proposal is what the plugin will do once it's accepted, as the user's
// shown it: a line for the whole (Title: "3 issues in RAM"), and its
// items, each accepted or not. The plugin is trusted to do what it says
// here, and only that.
type Proposal struct {
	Title string         `json:"title"`
	Items []ProposalItem `json:"items"`
}

// ProposalItem is one thing to be done: what it's called, the thing it's
// done to (On, a name: RAM-12; "" for one made), what it'll be, row by
// row, and at length (Markdown), and Payload, the plugin's own, which ws
// keeps and sends back as it was when the item's accepted. An item names
// an earlier one of the same proposal as #1, #2…; what it made is given
// when it's accepted.
type ProposalItem struct {
	Title    string          `json:"title"`
	On       string          `json:"on,omitempty"`
	Rows     []ProposalRow   `json:"rows,omitempty"`
	Markdown string          `json:"markdown,omitempty"`
	Payload  json.RawMessage `json:"payload"`
}

// ProposalRow is a label and what it'll be, and, for a change to
// something that's there, what it was (Was, nil for a row that isn't a
// change).
type ProposalRow struct {
	Label string  `json:"label"`
	Value string  `json:"value"`
	Was   *string `json:"was,omitempty"`
}

// ProposalAcceptParams is the user accepting items of what was drafted
// (ID): by their number in it, from 1, each with its payload, in order.
// Made is what items accepted before made, by number, for an item that
// names one (#1). Key is ws's for the proposal, the same each time it's
// asked: an item's is Key/N, and one that was made already isn't made
// again.
type ProposalAcceptParams struct {
	ID           string         `json:"id"`
	Key          string         `json:"key"`
	Items        []AcceptItem   `json:"items"`
	Made         map[int]string `json:"made,omitempty"`
	Conversation string         `json:"conversation"`
	Mission      string         `json:"mission,omitempty"`
}

// AcceptItem is one item accepted: its number in the proposal, and its
// payload as drafted.
type AcceptItem struct {
	N       int             `json:"n"`
	Payload json.RawMessage `json:"payload"`
}

// ProposalAcceptResult is what came of each item asked, in order.
type ProposalAcceptResult struct {
	Items []AcceptedItem `json:"items"`
}

// AcceptedItem is what came of one item: Made, the name of what it made
// or changed (RAM-130), and Say, a line on it; or Problem, why it wasn't
// done (it's left to be accepted again). Open is a path in the plugin's
// app to see it at.
type AcceptedItem struct {
	N       int    `json:"n"`
	Made    string `json:"made,omitempty"`
	Say     string `json:"say,omitempty"`
	Open    string `json:"open,omitempty"`
	Problem string `json:"problem,omitempty"`
}
