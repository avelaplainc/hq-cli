package main

import (
	"io"
	"os"
	"strings"

	"github.com/mattn/go-isatty"

	"hq/pkg/tree"
)

// ANSI SGR codes for the output palette: blue keys, green strings, bright
// white scalars. An empty palette prints plain text.
type palette struct {
	key    string
	str    string
	scalar string
	reset  string
}

var plainPalette = palette{}
var colorPalette = palette{
	key:    "\x1b[1;34m", // bold blue, like jq object keys
	str:    "\x1b[0;32m", // green, like jq strings
	scalar: "\x1b[0;97m", // bright white
	reset:  "\x1b[0m",
}

// wrap paints s with code when the palette has colors.
func (p palette) wrap(code, s string) string {
	if code == "" {
		return s
	}
	return code + s + p.reset
}

// colorForce holds the -C and -M command line state: forceNone when no
// flag was given, forceColor for -C, forceMono for -M. Like jq, -M beats
// -C when both are given.
type colorForce int

const (
	forceNone  colorForce = 0
	forceColor colorForce = 1
	forceMono  colorForce = -1
)

// colorEnabled decides colors the way jq does (src/main.c of jqlang/jq):
// the default wants a terminal on stdout and a TERM that is set, not empty,
// and not "dumb"; NO_COLOR removes the default; -C forces color on; -M is
// applied last and forces color off. It adds HQ_COLOR, CLICOLOR_FORCE, and
// CLICOLOR as extra switches.
func colorEnabled(out io.Writer, force colorForce) bool {
	switch force {
	case forceColor:
		return true
	case forceMono:
		return false
	}
	if v := os.Getenv("HQ_COLOR"); v != "" {
		return v != "0"
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	term := os.Getenv("TERM")
	if term == "" || term == "dumb" {
		return false
	}
	if v := os.Getenv("CLICOLOR_FORCE"); v != "" && v != "0" {
		return true
	}
	if os.Getenv("CLICOLOR") == "0" {
		return false
	}
	f, ok := out.(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(f.Fd())
}

// paletteFor returns the palette for this run.
func paletteFor(out io.Writer, force colorForce) palette {
	if colorEnabled(out, force) {
		return colorPalette
	}
	return plainPalette
}

// renderJSON writes the node as pretty JSON with two-space indent. The
// palette paints keys, strings, and scalars; punctuation stays plain. The
// output keeps source key order and does not escape <, >, and &.
func renderJSON(n *tree.Node, p palette) string {
	var sb strings.Builder
	writeJSON(&sb, n, p, 0)
	sb.WriteByte('\n')
	return sb.String()
}

// renderJSONCompact writes the node as one line of JSON, in the same
// colors as renderJSON.
func renderJSONCompact(n *tree.Node, p palette) string {
	var sb strings.Builder
	writeJSONCompact(&sb, n, p)
	return sb.String()
}

func writeJSONCompact(sb *strings.Builder, n *tree.Node, p palette) {
	if n == nil {
		sb.WriteString("null")
		return
	}
	switch n.Kind {
	case tree.KindNull:
		sb.WriteString(p.wrap(p.scalar, "null"))
	case tree.KindBool:
		if n.Bool {
			sb.WriteString(p.wrap(p.scalar, "true"))
		} else {
			sb.WriteString(p.wrap(p.scalar, "false"))
		}
	case tree.KindNumber:
		num := string(n.Num)
		if num == "" {
			num = "0"
		}
		sb.WriteString(p.wrap(p.scalar, num))
	case tree.KindString:
		sb.WriteString(p.wrap(p.str, jsonString(n.Str)))
	case tree.KindArray:
		sb.WriteByte('[')
		for i, it := range n.Items {
			if i > 0 {
				sb.WriteByte(',')
			}
			writeJSONCompact(sb, it, p)
		}
		sb.WriteByte(']')
	case tree.KindObject:
		sb.WriteByte('{')
		for i, f := range n.Fields {
			if i > 0 {
				sb.WriteByte(',')
			}
			sb.WriteString(p.wrap(p.key, jsonString(f.Key)))
			sb.WriteByte(':')
			writeJSONCompact(sb, f.Val, p)
		}
		sb.WriteByte('}')
	default:
		sb.WriteString("null")
	}
}

func writeJSON(sb *strings.Builder, n *tree.Node, p palette, depth int) {
	if n == nil {
		sb.WriteString("null")
		return
	}
	indent := strings.Repeat("  ", depth)
	inner := strings.Repeat("  ", depth+1)
	switch n.Kind {
	case tree.KindNull:
		sb.WriteString(p.wrap(p.scalar, "null"))
	case tree.KindBool:
		if n.Bool {
			sb.WriteString(p.wrap(p.scalar, "true"))
		} else {
			sb.WriteString(p.wrap(p.scalar, "false"))
		}
	case tree.KindNumber:
		num := string(n.Num)
		if num == "" {
			num = "0"
		}
		sb.WriteString(p.wrap(p.scalar, num))
	case tree.KindString:
		sb.WriteString(p.wrap(p.str, jsonString(n.Str)))
	case tree.KindArray:
		if len(n.Items) == 0 {
			sb.WriteString("[]")
			return
		}
		sb.WriteString("[\n")
		for i, it := range n.Items {
			if i > 0 {
				sb.WriteString(",\n")
			}
			sb.WriteString(inner)
			writeJSON(sb, it, p, depth+1)
		}
		sb.WriteString("\n" + indent + "]")
	case tree.KindObject:
		if len(n.Fields) == 0 {
			sb.WriteString("{}")
			return
		}
		sb.WriteString("{\n")
		for i, f := range n.Fields {
			if i > 0 {
				sb.WriteString(",\n")
			}
			sb.WriteString(inner)
			sb.WriteString(p.wrap(p.key, jsonString(f.Key)))
			sb.WriteString(": ")
			writeJSON(sb, f.Val, p, depth+1)
		}
		sb.WriteString("\n" + indent + "}")
	default:
		sb.WriteString("null")
	}
}

// jsonString quotes and escapes s as JSON text. It does not escape <, >,
// and &.
func jsonString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString("\\\"")
		case '\\':
			sb.WriteString("\\\\")
		case '\n':
			sb.WriteString("\\n")
		case '\r':
			sb.WriteString("\\r")
		case '\t':
			sb.WriteString("\\t")
		default:
			if r < 0x20 {
				const hex = "0123456789abcdef"
				sb.WriteString("\\u00")
				sb.WriteByte(hex[r>>4])
				sb.WriteByte(hex[r&0xF])
			} else {
				sb.WriteRune(r)
			}
		}
	}
	sb.WriteByte('"')
	return sb.String()
}
