package plugin

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/withworkspace/plugin/protocol"
)

// A table is a config key holding named rows, each with the same fields:
// the queries a board shows, the projects it knows.
//
//	queries := p.Config.Table("queries", "Queries", plugin.RowName("Query"))
//	label := queries.String("label", "Label")
//	jql := queries.String("query", "JQL", plugin.Required)
//	…
//	for _, row := range queries.Rows() {
//		show(row.Name, label.In(row), jql.In(row))
//	}
//
// In ws.toml each row is a table under the key, named by the row:
//
//	[plugin.jira.queries.bugs]
//	label = "Bugs"
//	query = 'project = RAM AND type = Bug'
//
// ws checks each row against the fields (one that isn't declared, a value
// of the wrong type and a required one left out are each said), lays
// yours over the team's row by row and field by field, and gives the rows
// in the order they were written. A field is declared with the table's
// methods, as a key is with Config's, and each returns what reads it in a
// row. A field may be a table itself (Table), one level down and no
// deeper.

// RowName is what a table's rows are named by, in words ("Query", "Space
// key"): what someone adding a row is asked for.
func RowName(text string) Option { return func(k *protocol.ConfigKey) { k.Row = text } }

// TableKey reads a key holding named rows, and declares what's in a row.
type TableKey struct {
	c *Config
	// key is the config key it is: nil for a table that's a row's field.
	key *key
	// in is the table whose rows it's a field of, and name its name
	// there: for one that's a row's field.
	in     *TableKey
	name   string
	fields []tableField
}

// tableField is one of a row's fields: its spec, or the table it is.
type tableField struct {
	spec  protocol.ConfigKey
	table *TableKey
}

// Row is one row of a table: its name, and its fields, read with the
// handles the table's methods returned (label.In(row)).
type Row struct {
	Name   string
	values map[string]json.RawMessage
	of     *TableKey
}

// Table declares a key holding named rows.
func (c *Config) Table(key, title string, opts ...Option) *TableKey {
	t := &TableKey{c: c}
	t.key = c.declare(key, title, protocol.TypeTable, opts)
	t.key.table = t
	return t
}

// Rows are the table's rows, in the order they were written. A ws from
// before tables gives them with no order: they're then by name.
func (t *TableKey) Rows() []Row {
	if t.key == nil {
		return nil // a row's field: RowsIn reads it
	}
	v, ok := t.key.raw()
	if !ok {
		return nil
	}
	return t.rows(v)
}

// IsSet is whether the table has a row.
func (t *TableKey) IsSet() bool { return len(t.Rows()) > 0 }

// RowsIn are the rows of a table that's a row's field, in that row.
func (t *TableKey) RowsIn(r Row) []Row {
	if t.in == nil || !t.in.owns(r, t.name) {
		return nil
	}
	v, ok := r.values[t.name]
	if !ok || blank(v) {
		return nil
	}
	return t.rows(v)
}

// rows reads a table's value: its rows as ws sends them, or, from a ws
// that passes the file through as it is, an object of objects by name.
func (t *TableKey) rows(v json.RawMessage) []Row {
	var sent []protocol.ConfigRow
	if json.Unmarshal(v, &sent) == nil {
		out := make([]Row, 0, len(sent))
		for _, r := range sent {
			out = append(out, Row{Name: r.Name, values: r.Values, of: t})
		}
		return out
	}
	var byName map[string]map[string]json.RawMessage
	if json.Unmarshal(v, &byName) != nil {
		return nil
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]Row, 0, len(names))
	for _, name := range names {
		out = append(out, Row{Name: name, values: byName[name], of: t})
	}
	return out
}

// owns is whether r is one of t's rows, which a field of t's is read in:
// a field read in another table's row is a mistake in the plugin, said
// once in its log, and reads as not set.
func (t *TableKey) owns(r Row, field string) bool {
	if r.of == t {
		return true
	}
	t.c.p.Log.Error(fmt.Sprintf("config: %s is a field of %s, and was read in a row of %s", field, t.path(), r.of.path()))
	return false
}

// path is the table's name as config has it: queries, spaces.custom_fields.
func (t *TableKey) path() string {
	switch {
	case t == nil:
		return "no table"
	case t.in != nil:
		return t.in.path() + "." + t.name
	}
	return t.key.spec.Key
}

// fieldSpecs are the table's fields as ws is told them.
func (t *TableKey) fieldSpecs() []protocol.ConfigKey {
	out := make([]protocol.ConfigKey, 0, len(t.fields))
	for _, f := range t.fields {
		s := f.spec
		if f.table != nil {
			s.Fields = f.table.fieldSpecs()
		}
		out = append(out, s)
	}
	return out
}

// declareField declares one of a row's fields.
func (t *TableKey) declareField(name, title, typ string, opts []Option) *rowField {
	t.c.p.declaring("Config")
	spec := protocol.ConfigKey{Key: name, Title: title, Type: typ}
	for _, o := range opts {
		o(&spec)
	}
	spec.Scope = "" // a row is the team's or yours whole: its fields aren't one or the other
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	for _, was := range t.fields {
		if was.spec.Key == name {
			t.c.p.declErrs = append(t.c.p.declErrs, fmt.Errorf("config table %s: field %q is declared twice", t.path(), name))
		}
	}
	t.fields = append(t.fields, tableField{spec: spec})
	return &rowField{t: t, spec: spec}
}

// rowField is one declared field of a table's rows.
type rowField struct {
	t    *TableKey
	spec protocol.ConfigKey
}

// raw is the field's value in r, and whether it's set there.
func (f *rowField) raw(r Row) (json.RawMessage, bool) {
	if !f.t.owns(r, f.spec.Key) {
		return nil, false
	}
	v, ok := r.values[f.spec.Key]
	return v, ok && !blank(v)
}

// IsSetIn is whether the field has a value of its own in r, not its
// default.
func (f *rowField) IsSetIn(r Row) bool {
	_, ok := f.raw(r)
	return ok
}

// String declares a field holding a line of text.
func (t *TableKey) String(name, title string, opts ...Option) *StringField {
	return &StringField{t.declareField(name, title, protocol.TypeString, opts)}
}

// URL declares a field holding a URL.
func (t *TableKey) URL(name, title string, opts ...Option) *StringField {
	return &StringField{t.declareField(name, title, protocol.TypeURL, opts)}
}

// Enum declares a field holding one of options.
func (t *TableKey) Enum(name, title string, options []string, opts ...Option) *StringField {
	f := t.declareField(name, title, protocol.TypeEnum, opts)
	f.spec.Options = options
	t.c.mu.Lock()
	t.fields[len(t.fields)-1].spec.Options = options
	t.c.mu.Unlock()
	return &StringField{f}
}

// List declares a field holding a list of strings.
func (t *TableKey) List(name, title string, opts ...Option) *ListField {
	return &ListField{t.declareField(name, title, protocol.TypeList, opts)}
}

// Bool declares a field that's on or off.
func (t *TableKey) Bool(name, title string, opts ...Option) *BoolField {
	return &BoolField{t.declareField(name, title, protocol.TypeBool, opts)}
}

// Number declares a field holding a number.
func (t *TableKey) Number(name, title string, opts ...Option) *NumberField {
	return &NumberField{t.declareField(name, title, protocol.TypeNumber, opts)}
}

// Map declares a field holding text by text.
func (t *TableKey) Map(name, title string, opts ...Option) *MapField {
	return &MapField{t.declareField(name, title, protocol.TypeMap, opts)}
}

// Table declares a field that's a table itself: rows within each row,
// read with RowsIn. Its own fields can't be tables.
func (t *TableKey) Table(name, title string, opts ...Option) *TableKey {
	f := t.declareField(name, title, protocol.TypeTable, opts)
	nested := &TableKey{c: t.c, in: t, name: name}
	t.c.mu.Lock()
	defer t.c.mu.Unlock()
	if t.in != nil {
		t.c.p.declErrs = append(t.c.p.declErrs, fmt.Errorf("config table %s: %q is a table in a table in a table, and a table goes one level down", t.path(), f.spec.Key))
	}
	t.fields[len(t.fields)-1].table = nested
	return nested
}

// StringField reads a row's field holding text: a string, URL or enum.
type StringField struct{ *rowField }

// In is the field's value in r: what's set, else its default, else "".
func (f *StringField) In(r Row) string {
	v, ok := f.raw(r)
	return textOf(v, ok, f.spec.Default)
}

// ListField reads a row's field holding a list of strings.
type ListField struct{ *rowField }

// In is the field's value in r: what's set, else its default, else none.
func (f *ListField) In(r Row) []string {
	v, ok := f.raw(r)
	return listOf(v, ok, f.spec.Default)
}

// BoolField reads a row's field that's on or off.
type BoolField struct{ *rowField }

// In is the field's value in r: what's set, else its default, else
// false.
func (f *BoolField) In(r Row) bool {
	v, ok := f.raw(r)
	return boolOf(v, ok, f.spec.Default)
}

// NumberField reads a row's field holding a number.
type NumberField struct{ *rowField }

// In is the field's value in r: what's set, else its default, else 0.
func (f *NumberField) In(r Row) float64 {
	v, ok := f.raw(r)
	return numberOf(v, ok, f.spec.Default)
}

// MapField reads a row's field holding text by text.
type MapField struct{ *rowField }

// In is the field's value in r: what's set, else its default, else none.
func (f *MapField) In(r Row) map[string]string {
	v, ok := f.raw(r)
	return mapOf(v, ok, f.spec.Default)
}
