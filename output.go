package plugin

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Markdowner is a value that says how an agent should read it: Show
// prints its Markdown for -f llm.
type Markdowner interface {
	Markdown() string
}

// Say prints a line. For -f json it's {"message": text}.
func (c *Call) Say(text string) error {
	if c.format == FormatJSON {
		return c.json(map[string]string{"message": text})
	}
	_, err := fmt.Fprintln(c.out, text)
	return err
}

// List prints rows: a slice of structs (or pointers to them, or maps).
// For a person it's a table sized to their terminal; for -f json, the
// rows as JSON; for -f llm, a markdown table.
//
// A struct's exported fields are the columns. A field's `ws` tag names
// its column, and `ws:"-"` leaves it out of the table (JSON follows the
// `json` tags, as ever).
func (c *Call) List(rows any) error {
	if c.format == FormatJSON {
		v := reflect.ValueOf(rows)
		if v.Kind() == reflect.Slice && v.Len() == 0 {
			_, err := fmt.Fprintln(c.out, "[]")
			return err
		}
		return c.json(rows)
	}
	cols, cells, err := tabulate(rows)
	if err != nil {
		return err
	}
	if len(cells) == 0 {
		_, err := fmt.Fprintln(c.out, "Nothing to show.")
		return err
	}
	if c.format == FormatLLM {
		_, err := fmt.Fprint(c.out, markdownTable(cols, cells))
		return err
	}
	_, err = fmt.Fprint(c.out, textTable(cols, cells, c.Width(), c.term.Color))
	return err
}

// Show prints one thing: a struct (or a pointer to one, or a map), a
// field to a line. For -f json it's the value as JSON; for -f llm, a
// markdown list, or the value's own Markdown if it has one.
func (c *Call) Show(one any) error {
	if c.format == FormatJSON {
		return c.json(one)
	}
	if m, ok := one.(Markdowner); ok && c.format == FormatLLM {
		_, err := fmt.Fprintln(c.out, strings.TrimRight(m.Markdown(), "\n"))
		return err
	}
	cols, cells, err := tabulate([]any{one})
	if err != nil {
		return err
	}
	if len(cells) == 0 {
		return nil
	}
	var b strings.Builder
	width := 0
	for _, col := range cols {
		width = max(width, utf8.RuneCountInString(col))
	}
	for i, col := range cols {
		if c.format == FormatLLM {
			fmt.Fprintf(&b, "- **%s**: %s\n", col, cells[0][i])
		} else {
			fmt.Fprintf(&b, "%-*s  %s\n", width+1, col+":", cells[0][i])
		}
	}
	_, err = fmt.Fprint(c.out, b.String())
	return err
}

func (c *Call) json(v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(c.out, string(data))
	return err
}

// tabulate turns rows into columns' titles and each row's cells.
func tabulate(rows any) (cols []string, cells [][]string, err error) {
	v := reflect.ValueOf(rows)
	if v.Kind() != reflect.Slice && v.Kind() != reflect.Array {
		return nil, nil, fmt.Errorf("List takes a slice, not %T", rows)
	}
	var fields []int // of a struct's fields, the ones shown
	var keys []string
	for i := 0; i < v.Len(); i++ {
		row := v.Index(i)
		for row.Kind() == reflect.Pointer || row.Kind() == reflect.Interface {
			if row.IsNil() {
				break
			}
			row = row.Elem()
		}
		switch row.Kind() {
		case reflect.Struct:
			if cols == nil {
				cols, fields = structColumns(row.Type())
			}
			line := make([]string, len(fields))
			for j, f := range fields {
				line[j] = cell(row.Field(f))
			}
			cells = append(cells, line)
		case reflect.Map:
			if cols == nil {
				for _, k := range row.MapKeys() {
					keys = append(keys, fmt.Sprint(k.Interface()))
				}
				sort.Strings(keys)
				cols = keys
			}
			line := make([]string, len(keys))
			for j, k := range keys {
				if val := row.MapIndex(reflect.ValueOf(k)); val.IsValid() {
					line[j] = cell(val)
				}
			}
			cells = append(cells, line)
		default:
			if cols == nil {
				cols = []string{"Value"}
			}
			cells = append(cells, []string{cell(row)})
		}
	}
	return cols, cells, nil
}

// structColumns is a struct's columns: its exported fields, named by
// their ws tags, less those tagged "-".
func structColumns(t reflect.Type) (cols []string, fields []int) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := f.Name
		if tag, ok := f.Tag.Lookup("ws"); ok {
			if tag == "-" {
				continue
			}
			if tag != "" {
				name = tag
			}
		}
		cols = append(cols, name)
		fields = append(fields, i)
	}
	return cols, fields
}

// cell is a value as a table shows it.
func cell(v reflect.Value) string {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		return ""
	}
	switch x := v.Interface().(type) {
	case time.Time:
		if x.IsZero() {
			return ""
		}
		return x.Local().Format("2006-01-02 15:04")
	case bool:
		if x {
			return "yes"
		}
		return "no"
	case []string:
		return strings.Join(x, ", ")
	case fmt.Stringer:
		return x.String()
	}
	return strings.ReplaceAll(fmt.Sprint(v.Interface()), "\n", " ")
}

// textTable is a table for a terminal: columns padded, the widest cut
// short if the whole is wider than width (0 for no limit).
func textTable(cols []string, cells [][]string, width int, color bool) string {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = utf8.RuneCountInString(c)
	}
	for _, row := range cells {
		for i, c := range row {
			widths[i] = max(widths[i], utf8.RuneCountInString(c))
		}
	}
	const gap, least = 2, 8
	if width > 0 {
		for {
			total, widest := gap*(len(widths)-1), 0
			for i, w := range widths {
				total += w
				if w > widths[widest] {
					widest = i
				}
			}
			if total <= width || widths[widest] <= least {
				break
			}
			widths[widest] = max(least, widths[widest]-(total-width))
		}
	}
	var b strings.Builder
	line := func(row []string, bold bool) {
		for i, c := range row {
			c = clip(c, widths[i])
			if i < len(row)-1 {
				c += strings.Repeat(" ", widths[i]-utf8.RuneCountInString(c)+gap)
			}
			if bold {
				c = "\x1b[1m" + c + "\x1b[0m"
			}
			b.WriteString(c)
		}
		b.WriteString("\n")
	}
	heads := make([]string, len(cols))
	for i, c := range cols {
		heads[i] = strings.ToUpper(c)
	}
	line(heads, color)
	for _, row := range cells {
		line(row, false)
	}
	return b.String()
}

// clip is s cut to n characters, its end an ellipsis if it was longer.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// markdownTable is a table as markdown.
func markdownTable(cols []string, cells [][]string) string {
	var b strings.Builder
	esc := func(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
	b.WriteString("|")
	for _, c := range cols {
		b.WriteString(" " + esc(c) + " |")
	}
	b.WriteString("\n|")
	for range cols {
		b.WriteString(" --- |")
	}
	b.WriteString("\n")
	for _, row := range cells {
		b.WriteString("|")
		for _, c := range row {
			b.WriteString(" " + esc(c) + " |")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// relTime is a time as a person would say it: "2 minutes ago".
func relTime(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	d := time.Since(t)
	future := d < 0
	if future {
		d = -d
	}
	var s string
	switch {
	case d < 45*time.Second:
		return "just now"
	case d < 90*time.Second:
		s = "a minute"
	case d < time.Hour:
		s = fmt.Sprintf("%d minutes", int(d.Minutes()+0.5))
	case d < 90*time.Minute:
		s = "an hour"
	case d < 24*time.Hour:
		s = fmt.Sprintf("%d hours", int(d.Hours()+0.5))
	case d < 48*time.Hour:
		s = "a day"
	case d < 30*24*time.Hour:
		s = fmt.Sprintf("%d days", int(d.Hours()/24+0.5))
	default:
		return t.Local().Format("2 Jan 2006")
	}
	if future {
		return "in " + s
	}
	return s + " ago"
}
