package api

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode"
)

// rustFile is the native app's copy of the API types (native/), generated from
// this package from the same list as the TypeScript one, so neither client can
// drift from the daemon.
const rustFile = "../../native/crates/agentbox-api/src/types.rs"

// rustNames renames the Go types whose names Rust reserves or that would
// shadow the prelude.
var rustNames = map[string]string{"Self": "SelfInfo", "Error": "ApiError"}

func TestRustTypesAreUpToDate(t *testing.T) {
	want := rustTypes()
	if os.Getenv("UPDATE_TS") != "" {
		if err := os.WriteFile(rustFile, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(rustFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s is out of date: run UPDATE_TS=1 go test ./internal/api", rustFile)
	}
}

func TestRustSnake(t *testing.T) {
	for in, want := range map[string]string{
		"createdAt": "created_at", "openCodeAccounts": "open_code_accounts", "prURL": "pr_url",
		"ID": "id", "PRStateOpen": "pr_state_open", "ip": "ip", "image2x": "image2x", "x-y": "x_y",
	} {
		if got := rustSnake(in); got != want {
			t.Errorf("rustSnake(%q) = %q, want %q", in, got, want)
		}
	}
}

// rustTypes renders every type as a serde struct. The daemon's JSON is Go's:
// a nil slice or map is null and a missing field is its zero value, so every
// struct defaults its fields and reads null as empty; only pointers become
// Option. Unknown fields are ignored, so an older app reads a newer daemon.
func rustTypes() string {
	var b strings.Builder
	b.WriteString("// Generated from internal/api/types.go. Don't edit: run UPDATE_TS=1 go test ./internal/api\n")
	b.WriteString("use serde::{Deserialize, Serialize};\nuse std::collections::BTreeMap;\n\nuse crate::de::*;\n")
	for _, v := range tsTypes {
		t := reflect.TypeOf(v)
		b.WriteString("\n#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]\n#[serde(default)]\n")
		fmt.Fprintf(&b, "pub struct %s {\n", rustName(t))
		writeRustFields(&b, t, t, map[string]bool{})
		b.WriteString("}\n")
	}
	b.WriteString("\n")
	for _, c := range tsConstants {
		name := strings.ToUpper(rustSnake(c[0]))
		// rustfmt breaks a long one after the =, unless that doesn't fit either.
		if line := fmt.Sprintf("pub const %s: &str = %q;", name, c[1]); len(line) <= 100 || len(fmt.Sprintf("    %q;", c[1])) > 100 {
			b.WriteString(line + "\n")
		} else {
			fmt.Fprintf(&b, "pub const %s: &str =\n    %q;\n", name, c[1])
		}
	}
	return b.String()
}

func rustName(t reflect.Type) string {
	if n, ok := rustNames[t.Name()]; ok {
		return n
	}
	return t.Name()
}

func writeRustFields(b *strings.Builder, owner, t reflect.Type, used map[string]bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" || !f.IsExported() {
			continue
		}
		if f.Anonymous && name == "" && f.Type.Kind() == reflect.Struct {
			writeRustFields(b, owner, f.Type, used)
			continue
		}
		if name == "" {
			name = f.Name
		}
		field := rustSnake(name)
		for used[field] {
			field += "_"
		}
		used[field] = true
		var attrs []string
		if field != name {
			attrs = append(attrs, fmt.Sprintf("rename = %q", name))
		}
		ty := rustType(f.Type)
		if f.Type.Kind() == reflect.Pointer && f.Type.Elem() == owner {
			ty = "Option<Box<" + rustName(owner) + ">>"
		}
		omit := strings.Contains(opts, "omitempty")
		switch k := f.Type.Kind(); {
		case k == reflect.Pointer:
			attrs = append(attrs, `skip_serializing_if = "Option::is_none"`)
		case k == reflect.Slice && f.Type != reflect.TypeFor[json.RawMessage](), k == reflect.Map:
			attrs = append(attrs, `deserialize_with = "nullable"`)
			if omit {
				attrs = append(attrs, `skip_serializing_if = "is_empty"`)
			}
		case omit && (k == reflect.String || k == reflect.Bool || k == reflect.Int || k == reflect.Int64 || k == reflect.Uint16 || k == reflect.Float64 ||
			f.Type == reflect.TypeFor[time.Time]() || f.Type == reflect.TypeFor[json.RawMessage]() || k == reflect.Interface):
			attrs = append(attrs, `skip_serializing_if = "is_empty"`)
		}
		// Laid out as rustfmt would (attr_fn_like_width is 70), so cargo fmt
		// leaves the file alone.
		if args := strings.Join(attrs, ", "); len(attrs) > 0 && len(args) <= 70 {
			fmt.Fprintf(b, "    #[serde(%s)]\n", args)
		} else if len(attrs) > 0 {
			fmt.Fprintf(b, "    #[serde(\n        %s\n    )]\n", strings.Join(attrs, ",\n        "))
		}
		fmt.Fprintf(b, "    pub %s: %s,\n", rustIdent(field), ty)
	}
}

func rustType(t reflect.Type) string {
	switch t {
	case reflect.TypeFor[time.Time]():
		return "String"
	case reflect.TypeFor[json.RawMessage]():
		return "serde_json::Value"
	}
	switch t.Kind() {
	case reflect.Pointer:
		return "Option<" + rustType(t.Elem()) + ">"
	case reflect.String:
		return "String"
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int64:
		return "i64"
	case reflect.Uint16:
		return "u16"
	case reflect.Float64:
		return "f64"
	case reflect.Slice:
		return "Vec<" + rustType(t.Elem()) + ">"
	case reflect.Map:
		return "BTreeMap<" + rustType(t.Key()) + ", " + rustType(t.Elem()) + ">"
	case reflect.Struct:
		return rustName(t)
	case reflect.Interface:
		return "serde_json::Value"
	}
	panic("no Rust type for " + t.String())
}

// rustSnake turns a JSON or Go name into snake_case, keeping acronyms whole:
// prURL is pr_url, PRStateOpen pr_state_open.
func rustSnake(s string) string {
	r := []rune(s)
	var out strings.Builder
	for i, c := range r {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) {
			out.WriteRune('_')
			continue
		}
		if unicode.IsUpper(c) && i > 0 {
			prev := r[i-1]
			nextLower := i+1 < len(r) && unicode.IsLower(r[i+1])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
				out.WriteRune('_')
			}
		}
		out.WriteRune(unicode.ToLower(c))
	}
	return out.String()
}

func rustIdent(s string) string {
	switch s {
	case "self", "super", "crate":
		return s + "_"
	case "as", "async", "await", "break", "const", "continue", "dyn", "else", "enum", "extern", "false", "fn", "for", "gen",
		"if", "impl", "in", "let", "loop", "match", "mod", "move", "mut", "pub", "ref", "return", "static", "struct", "trait",
		"true", "try", "type", "unsafe", "use", "where", "while", "abstract", "become", "box", "do", "final", "macro",
		"override", "priv", "typeof", "unsized", "virtual", "yield":
		return "r#" + s
	}
	return s
}
