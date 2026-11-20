package plugin

import (
	"encoding"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
)

// Query declares that an endpoint that's read (Get, Delete) is asked with
// a query: what's after the path's ?. fn is given it as a Go type, as an
// endpoint that takes a body is given the body:
//
//	// Since is which of the history is asked for.
//	type Since struct {
//		Key   string `json:"key,omitempty"`
//		Limit int    `json:"limit,omitempty"`
//	}
//
//	plugin.Get(p, "/api/history", plugin.Query(func(r *http.Request, q Since) (History, error) {
//		return history(r.Context(), q.Key, q.Limit)
//	}))
//
// The app asks plugin.get("/api/history", { query: { limit: 300 } }), and
// a name the query hasn't, or a value of the wrong type, doesn't compile.
// An endpoint declared without Query is asked with no query at all.
//
// The query's type is a struct, exported as any type the app sends is.
// Each field is a string, a number, a bool, a type that reads itself from
// text (time.Time), a pointer to one of those, or a slice of them, which
// is the name given more than once (?label=a&label=b). A field goes by
// its json name, as it does in a body, and one marked omitempty may be
// left out by the app. A name that isn't sent leaves its field as it was:
// empty, 0, false, nil. One the query doesn't have is ignored. A value
// that can't be read as its field (?limit=lots) is a 400, which says
// which.
//
// It's written where the endpoint is declared, as what Get or Delete is
// given: apigen reads the query's type there. What's sent with a body
// goes in the body.
func Query[Q, Out any](fn func(r *http.Request, query Q) (Out, error)) func(r *http.Request) (Out, error) {
	fields, err := queryFields(reflect.TypeFor[Q]())
	if err != nil {
		// The author's mistake, and the same on every request: said as
		// the plugin starts, and in its test.
		panic("plugin.Query: " + err.Error())
	}
	return func(r *http.Request) (Out, error) {
		var q Q
		if err := readQuery(r.URL.Query(), reflect.ValueOf(&q).Elem(), fields); err != nil {
			var none Out
			return none, Status(http.StatusBadRequest, fmt.Errorf("%s's query: %w", r.Pattern, err))
		}
		return fn(r, q)
	}
}

// queryField is a field of a query's type: the name it goes by, and where
// it is in the struct.
type queryField struct {
	name  string
	index []int
}

var textUnmarshaler = reflect.TypeFor[encoding.TextUnmarshaler]()

// queryFields are the fields of a query's type, each checked to be one a
// query can fill.
func queryFields(t reflect.Type) ([]queryField, error) {
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("%s isn't a struct: a query is a struct, with a field for each name", t)
	}
	var fields []queryField
	for _, f := range reflect.VisibleFields(t) {
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		// An embedded struct's fields are the query's own: they're
		// visible fields themselves.
		if f.Anonymous && f.Type.Kind() == reflect.Struct && name == "" && !reflect.PointerTo(f.Type).Implements(textUnmarshaler) {
			continue
		}
		if name == "" {
			name = f.Name
		}
		of := f.Type
		if of.Kind() == reflect.Slice {
			of = of.Elem()
		}
		if of.Kind() == reflect.Pointer {
			of = of.Elem()
		}
		if !queryValue(of) {
			return nil, fmt.Errorf("%s.%s is a %s: a query's field is a string, a number, a bool, a type that reads itself from text, a pointer to one, or a slice of them", t, f.Name, f.Type)
		}
		fields = append(fields, queryField{name: name, index: f.Index})
	}
	return fields, nil
}

// queryValue says whether one value of a query can be read as t.
func queryValue(t reflect.Type) bool {
	if reflect.PointerTo(t).Implements(textUnmarshaler) {
		return true
	}
	switch t.Kind() {
	case reflect.String, reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

// readQuery fills a query's struct from what was sent.
func readQuery(sent url.Values, into reflect.Value, fields []queryField) error {
	for _, f := range fields {
		values, ok := sent[f.name]
		if !ok || len(values) == 0 {
			continue
		}
		field, err := into.FieldByIndexErr(f.index)
		if err != nil {
			continue // in an embedded struct that's a nil pointer
		}
		if field.Kind() == reflect.Slice {
			list := reflect.MakeSlice(field.Type(), len(values), len(values))
			for i, v := range values {
				if err := readQueryValue(f.name, v, list.Index(i)); err != nil {
					return err
				}
			}
			field.Set(list)
			continue
		}
		if err := readQueryValue(f.name, values[0], field); err != nil {
			return err
		}
	}
	return nil
}

// readQueryValue reads one value of a query into a field.
func readQueryValue(name, value string, into reflect.Value) error {
	if into.Kind() == reflect.Pointer {
		into.Set(reflect.New(into.Type().Elem()))
		into = into.Elem()
	}
	if into.Addr().Type().Implements(textUnmarshaler) {
		if err := into.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(value)); err != nil {
			return fmt.Errorf("%s is %q, which can't be read: %w", name, value, err)
		}
		return nil
	}
	switch into.Kind() {
	case reflect.String:
		into.SetString(value)
	case reflect.Bool:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("%s is %q, which isn't true or false", name, value)
		}
		into.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(value, 10, into.Type().Bits())
		if err != nil {
			return fmt.Errorf("%s is %q, which isn't a whole number", name, value)
		}
		into.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(value, 10, into.Type().Bits())
		if err != nil {
			return fmt.Errorf("%s is %q, which isn't a whole number of 0 or more", name, value)
		}
		into.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(value, into.Type().Bits())
		if err != nil {
			return fmt.Errorf("%s is %q, which isn't a number", name, value)
		}
		into.SetFloat(n)
	}
	return nil
}
