package plugin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"sync"

	"github.com/withworkspace/plugin/protocol"
)

// Config is a plugin's config. A key is declared with one of its methods,
// which returns what reads it:
//
//	site := p.Config.URL("site", "Jira site", plugin.Required)
//	…
//	site.Get()
//
// ws validates the config, keeps it, asks for it on install and draws its
// form in Settings. A change reaches Get at once.
type Config struct {
	p *Plugin

	mu      sync.Mutex
	keys    []*key
	values  map[string]json.RawMessage
	version int
}

// key is one declared key.
type key struct {
	c    *Config
	spec protocol.ConfigKey
	// inSetup is whether a Setup read it: a change to it restarts the
	// plugin.
	inSetup bool
	// table is the key's rows' fields, when it's a table.
	table *TableKey
}

// Option says more about a config key.
type Option func(*protocol.ConfigKey)

// Required marks a key the plugin can't run without.
var Required Option = func(k *protocol.ConfigKey) { k.Required = true }

// Local marks a key as each person's own (kept in ws.local.toml), not the
// team's. A secret is always local.
var Local Option = func(k *protocol.ConfigKey) { k.Scope = protocol.ScopeLocal }

// Default is a key's value when it isn't set.
func Default(v any) Option { return func(k *protocol.ConfigKey) { k.Default = v } }

// Help is a line under the key's field in Settings.
func Help(text string) Option { return func(k *protocol.ConfigKey) { k.Help = text } }

// Example is shown in the key's field while it's empty.
func Example(text string) Option { return func(k *protocol.ConfigKey) { k.Example = text } }

// Version is the config's version: a whole number, raised when a change
// needs upgrade notes (upgrades/<n>.md).
func (c *Config) Version(n int) {
	c.p.declaring("Config.Version")
	c.version = n
}

func (c *Config) declare(name, title, typ string, opts []Option) *key {
	c.p.declaring("Config")
	k := &key{c: c, spec: protocol.ConfigKey{Key: name, Title: title, Type: typ}}
	for _, o := range opts {
		o(&k.spec)
	}
	if typ == protocol.TypeSecret {
		k.spec.Scope = protocol.ScopeLocal
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, was := range c.keys {
		if was.spec.Key == name {
			c.p.declErrs = append(c.p.declErrs, fmt.Errorf("config key %q is declared twice", name))
		}
	}
	c.keys = append(c.keys, k)
	return k
}

// String declares a key holding a line of text.
func (c *Config) String(key, title string, opts ...Option) *StringKey {
	return &StringKey{c.declare(key, title, protocol.TypeString, opts)}
}

// URL declares a key holding a URL.
func (c *Config) URL(key, title string, opts ...Option) *StringKey {
	return &StringKey{c.declare(key, title, protocol.TypeURL, opts)}
}

// Secret declares a key holding a secret: a token, a password. ws keeps
// it apart from the rest of the config, and it's never the team's.
func (c *Config) Secret(key, title string, opts ...Option) *StringKey {
	return &StringKey{c.declare(key, title, protocol.TypeSecret, opts)}
}

// Enum declares a key holding one of options.
func (c *Config) Enum(key, title string, options []string, opts ...Option) *StringKey {
	k := c.declare(key, title, protocol.TypeEnum, opts)
	k.spec.Options = options
	return &StringKey{k}
}

// List declares a key holding a list of strings.
func (c *Config) List(key, title string, opts ...Option) *ListKey {
	return &ListKey{c.declare(key, title, protocol.TypeList, opts)}
}

// Bool declares a key that's on or off.
func (c *Config) Bool(key, title string, opts ...Option) *BoolKey {
	return &BoolKey{c.declare(key, title, protocol.TypeBool, opts)}
}

// Number declares a key holding a number.
func (c *Config) Number(key, title string, opts ...Option) *NumberKey {
	return &NumberKey{c.declare(key, title, protocol.TypeNumber, opts)}
}

// Map declares a key holding text by text: a name for each of some
// things, as `columns = { "In Review" = "Review" }`.
func (c *Config) Map(key, title string, opts ...Option) *MapKey {
	return &MapKey{c.declare(key, title, protocol.TypeMap, opts)}
}

// raw is the key's value as ws gave it, and whether it's set. A read in
// a Setup is noted.
func (k *key) raw() (json.RawMessage, bool) {
	k.c.p.mu.Lock()
	inSetup := k.c.p.inSetup
	k.c.p.mu.Unlock()
	k.c.mu.Lock()
	defer k.c.mu.Unlock()
	if inSetup {
		k.inSetup = true
	}
	v, ok := k.c.values[k.spec.Key]
	return v, ok && !blank(v)
}

// IsSet is whether the key has a value of its own, not its default.
func (k *key) IsSet() bool {
	_, ok := k.raw()
	return ok
}

// blank is whether a value is as good as not set: null, "", [] or {}.
func blank(v json.RawMessage) bool {
	v = bytes.TrimSpace(v)
	return len(v) == 0 || bytes.Equal(v, []byte("null")) || bytes.Equal(v, []byte(`""`)) || bytes.Equal(v, []byte("[]")) || bytes.Equal(v, []byte("{}"))
}

// What a value is, given what's set (v, if ok) and the default its key
// was declared with: a key's and a row's field's are read the same way.

func textOf(v json.RawMessage, ok bool, def any) string {
	if ok {
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
	}
	s, _ := def.(string)
	return s
}

func listOf(v json.RawMessage, ok bool, def any) []string {
	if ok {
		var l []string
		if json.Unmarshal(v, &l) == nil {
			return l
		}
	}
	l, _ := def.([]string)
	return l
}

func boolOf(v json.RawMessage, ok bool, def any) bool {
	if ok {
		var b bool
		if json.Unmarshal(v, &b) == nil {
			return b
		}
	}
	b, _ := def.(bool)
	return b
}

func numberOf(v json.RawMessage, ok bool, def any) float64 {
	if ok {
		var n float64
		if json.Unmarshal(v, &n) == nil {
			return n
		}
		// A number kept as text ("5") is still a number.
		var s string
		if json.Unmarshal(v, &s) == nil {
			if n, err := strconv.ParseFloat(s, 64); err == nil {
				return n
			}
		}
	}
	switch d := def.(type) {
	case float64:
		return d
	case int:
		return float64(d)
	}
	return 0
}

func mapOf(v json.RawMessage, ok bool, def any) map[string]string {
	if ok {
		var m map[string]string
		if json.Unmarshal(v, &m) == nil {
			return m
		}
	}
	m, _ := def.(map[string]string)
	return m
}

// StringKey reads a key holding text: a string, URL, secret or enum.
type StringKey struct{ *key }

// Get is the key's value: what's set, else its default, else "".
func (k *StringKey) Get() string {
	v, ok := k.raw()
	return textOf(v, ok, k.spec.Default)
}

// ListKey reads a key holding a list of strings.
type ListKey struct{ *key }

// Get is the key's value: what's set, else its default, else none.
func (k *ListKey) Get() []string {
	v, ok := k.raw()
	return listOf(v, ok, k.spec.Default)
}

// BoolKey reads a key that's on or off.
type BoolKey struct{ *key }

// Get is the key's value: what's set, else its default, else false.
func (k *BoolKey) Get() bool {
	v, ok := k.raw()
	return boolOf(v, ok, k.spec.Default)
}

// NumberKey reads a key holding a number.
type NumberKey struct{ *key }

// Get is the key's value: what's set, else its default, else 0.
func (k *NumberKey) Get() float64 {
	v, ok := k.raw()
	return numberOf(v, ok, k.spec.Default)
}

// MapKey reads a key holding text by text: a name for each of some
// things.
type MapKey struct{ *key }

// Get is the key's value: what's set, else its default, else none.
func (k *MapKey) Get() map[string]string {
	v, ok := k.raw()
	return mapOf(v, ok, k.spec.Default)
}

// spec is the keys as ws is told them.
func (c *Config) spec() []protocol.ConfigKey {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]protocol.ConfigKey, 0, len(c.keys))
	for _, k := range c.keys {
		s := k.spec
		if k.table != nil {
			s.Fields = k.table.fieldSpecs()
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// set takes the whole config, and says whether a key a Setup read has
// changed.
func (c *Config) set(values map[string]json.RawMessage) (restart bool) {
	if values == nil {
		values = map[string]json.RawMessage{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range c.keys {
		if k.inSetup && !same(c.values[k.spec.Key], values[k.spec.Key]) {
			restart = true
		}
	}
	c.values = values
	return restart
}

// same is whether two values are the same JSON, give or take spacing.
func same(a, b json.RawMessage) bool {
	if blank(a) && blank(b) {
		return true
	}
	var x, y bytes.Buffer
	if json.Compact(&x, a) != nil || json.Compact(&y, b) != nil {
		return bytes.Equal(a, b)
	}
	return bytes.Equal(x.Bytes(), y.Bytes())
}

// complete is whether every required key is set.
func (c *Config) complete() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range c.keys {
		if k.spec.Required && blank(c.values[k.spec.Key]) {
			return false
		}
	}
	return true
}
