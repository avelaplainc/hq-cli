package main

import (
	"strings"

	"hq/pkg/tree"
)

// renderHCLColored writes the node as one line of HCL with the palette.
func renderHCLColored(n *tree.Node, p palette) string {
	return renderHCLP(n, p)
}

func renderHCLP(n *tree.Node, p palette) string {
	var sb strings.Builder
	writeHCL(&sb, n, p)
	return sb.String()
}

func writeHCL(sb *strings.Builder, n *tree.Node, p palette) {
	switch n.Kind {
	case tree.KindNull:
		sb.WriteString(p.wrap(p.scalar, "null"))
	case tree.KindBool:
		if n.Bool {
			sb.WriteString(p.wrap(p.scalar, "true"))
		} else {
			sb.WriteString(p.wrap(p.scalar, "false"))
		}
	case tree.KindString:
		sb.WriteString(p.wrap(p.str, hclString(n.Str)))
	case tree.KindNumber:
		num := string(n.Num)
		if num == "" {
			num = "0"
		}
		sb.WriteString(p.wrap(p.scalar, num))
	case tree.KindArray:
		sb.WriteByte('[')
		for i, it := range n.Items {
			if i > 0 {
				sb.WriteString(", ")
			}
			writeHCL(sb, it, p)
		}
		sb.WriteByte(']')
	case tree.KindObject:
		if len(n.Fields) == 0 {
			sb.WriteString("{}")
			return
		}
		sb.WriteString("{ ")
		for i, f := range n.Fields {
			if i > 0 {
				sb.WriteString(", ")
			}
			writeHCLKey(sb, f.Key, p)
			sb.WriteString(" = ")
			writeHCL(sb, f.Val, p)
		}
		sb.WriteString(" }")
	default:
		sb.WriteString("null")
	}
}

// writeHCLKey writes an object key: a bare identifier when the name allows
// it, else a quoted string.
func writeHCLKey(sb *strings.Builder, key string, p palette) {
	if isHCLIdent(key) {
		sb.WriteString(p.wrap(p.key, key))
		return
	}
	sb.WriteString(p.wrap(p.key, hclString(key)))
}

// isHCLIdent tells if the name is a valid HCL identifier.
func isHCLIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
			// always allowed
		case r >= '0' && r <= '9', r == '-':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// hclString quotes and escapes an HCL string. The template escape '$${'
// keeps literal '${' text from turning into an interpolation.
func hclString(s string) string {
	var sb strings.Builder
	sb.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
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
		case '$':
			if i+1 < len(s) && s[i+1] == '{' {
				sb.WriteString("$${")
				i++
			} else {
				sb.WriteByte('$')
			}
		default:
			sb.WriteByte(c)
		}
	}
	sb.WriteByte('"')
	return sb.String()
}
