package main

import (
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// stdinStat reads the file info of standard input. Tests replace it.
var stdinStat = func() (os.FileInfo, error) { return os.Stdin.Stat() }

// stdinIsPiped tells if standard input holds data from a pipe or a file.
func stdinIsPiped() bool {
	fi, err := stdinStat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice == 0
}

// formatHCL validates the input as HCL, then prints it the way
// terraform fmt does, plus one rule: every object with properties gets
// one line per property, even when the source writes it on one line.
// hclwrite.Format lays out the source lines, expandInlineObjects splits
// such objects, and a second Format pass aligns the new lines. Running
// it twice returns the same bytes.
func formatHCL(r io.Reader, w io.Writer, force colorForce) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m, err := analyzeHCL(data, "(stdin)")
	if err != nil {
		return err
	}
	formatted := hclwrite.Format(data)
	formatted = expandInlineObjects(formatted, m)
	formatted = hclwrite.Format(formatted)
	m, err = analyzeHCL(formatted, "(stdin)")
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, colorizeLines(string(formatted), m, paletteFor(w, force)))
	return err
}

// span is an object literal that opens and closes on one line.
type span struct {
	start   int // byte of '{'
	end     int // byte of '}'
	pbBase  int // open ( and [ count at '{'
	hasProp bool
	obj     bool
	commas  []int
}

// expandInlineObjects splits every object written on one line with one
// or more properties into one line per property. hclwrite.Format keeps
// such objects on one line and does not fix them. Heredoc content
// passes untouched, and objects without properties keep their layout.
func expandInlineObjects(src []byte, m *lineMap) []byte {
	out := make([]byte, 0, len(src))
	ln := 1
	start := 0
	prevc := byte(0) // last byte outside strings and comments on the previous line
	for i, c := range src {
		if c != '\n' {
			continue
		}
		line := string(src[start:i])
		if !m.heredoc[ln] {
			expanded, pv := expandLine(line, prevc)
			out = append(out, expanded...)
			prevc = pv
		} else {
			out = append(out, line...)
		}
		out = append(out, '\n')
		ln++
		start = i + 1
	}
	if start < len(src) {
		last := string(src[start:])
		if !m.heredoc[ln] {
			expanded, _ := expandLine(last, prevc)
			out = append(out, expanded...)
		} else {
			out = append(out, last...)
		}
	}
	return out
}

// expandLine rewrites each single-line object with properties: '{' on
// its own place, one property per line, '}' alone. The returned byte is
// the context for the next line, as from scanLine.
func expandLine(line string, prevc byte) (string, byte) {
	spans, prevc := scanLine(line, prevc)
	if len(spans) == 0 {
		return line, prevc
	}
	// Only expand objects that contain no other object. Nested objects
	// expand with their property.
	top := make([]span, 0, len(spans))
	for _, s := range spans {
		nested := false
		for _, o := range spans {
			if o.start < s.start && s.end < o.end {
				nested = true
				break
			}
		}
		if !nested {
			top = append(top, s)
		}
	}
	nl := "\n"
	if strings.HasSuffix(line, "\r") {
		nl = "\r\n"
	}
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	inner := indent + "  "
	var b strings.Builder
	pos := 0
	for _, s := range top {
		b.WriteString(line[pos:s.start])
		b.WriteString("{")
		b.WriteString(nl)
		props := make([]string, 0, len(s.commas)+1)
		last := s.start + 1
		for _, c := range s.commas {
			props = append(props, line[last:c])
			last = c + 1
		}
		props = append(props, line[last:s.end])
		first := true
		for _, p := range props {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			expanded, _ := expandLine(p, 0)
			for _, l := range strings.Split(expanded, "\n") {
				if l == "" {
					continue
				}
				if !first {
					b.WriteString(nl)
				}
				b.WriteString(inner)
				b.WriteString(l)
				first = false
			}
		}
		b.WriteString(nl)
		b.WriteString(indent)
		b.WriteString("}")
		pos = s.end + 1
	}
	b.WriteString(line[pos:])
	return b.String(), prevc
}

// scanLine returns the object literals that open and close on the line
// and carry one or more 'key = value' pairs. A '{' that opens a block
// header is skipped. The returned byte is the last non-space byte
// outside strings and comments: the context for a '{' that starts the
// next line.
func scanLine(line string, prevc byte) ([]span, byte) {
	var spans []span
	type frame struct {
		kind  byte // 'B' base, 'Q' quoted, 'S' single quote, 'I' interpolation
		depth int  // open brackets inside an 'I' frame
	}
	stack := []frame{{kind: 'B'}}
	braces := 0
	pb := 0
	var active []span
	i := 0
	for i < len(line) {
		c := line[i]
		f := &stack[len(stack)-1]
		switch f.kind {
		case 'B':
			if c == '#' || (c == '/' && i+1 < len(line) && line[i+1] == '/') {
				return spans, prevc
			}
			old := prevc
			if c != ' ' && c != '\t' && c != '\r' {
				prevc = c
			}
			switch {
			case c == '"':
				stack = append(stack, frame{kind: 'Q'})
			case c == '\'':
				stack = append(stack, frame{kind: 'S'})
			case c == '{':
				s := span{start: i, pbBase: pb, obj: braces > 0}
				if braces == 0 {
					s.obj = objByte(line, i, old)
				}
				braces++
				active = append(active, s)
			case c == '}':
				if braces > 0 {
					braces--
					s := active[len(active)-1]
					active = active[:len(active)-1]
					if s.obj && s.hasProp {
						s.end = i
						spans = append(spans, s)
					}
				}
			case c == '(' || c == '[':
				pb++
			case c == ')' || c == ']':
				if pb > 0 {
					pb--
				}
			case c == '=':
				if n := len(active); n > 0 {
					active[n-1].hasProp = true
				}
			case c == ',':
				if n := len(active); n > 0 && pb == active[n-1].pbBase {
					active[n-1].commas = append(active[n-1].commas, i)
				}
			}
			i++
		case 'Q':
			switch {
			case c == '\\':
				i += 2
			case c == '"':
				stack = stack[:len(stack)-1]
				i++
			case (c == '$' || c == '%') && i+1 < len(line) && line[i+1] == '{':
				stack = append(stack, frame{kind: 'I'})
				i += 2
			default:
				i++
			}
		case 'S':
			if c == '\'' {
				stack = stack[:len(stack)-1]
			}
			i++
		case 'I':
			switch {
			case c == '"':
				stack = append(stack, frame{kind: 'Q'})
			case c == '\'':
				stack = append(stack, frame{kind: 'S'})
			case c == '{' || c == '(' || c == '[':
				f.depth++
			case c == '}' || c == ')' || c == ']':
				if f.depth > 0 {
					f.depth--
				} else if c == '}' {
					stack = stack[:len(stack)-1]
				}
			}
			i++
		}
	}
	return spans, prevc
}

// objByte tells if the byte before a depth-zero '{' is the tail of an
// expression, so the brace opens an object and not a block header.
func objByte(line string, i int, prevc byte) bool {
	for j := i - 1; j >= 0; j-- {
		switch line[j] {
		case ' ', '\t', '\r':
		default:
			return isObjStart(line[j])
		}
	}
	return isObjStart(prevc)
}

func isObjStart(c byte) bool {
	switch c {
	case '=', ',', '[', '(', ':', '?':
		return true
	}
	return false
}

// colorizeLines paints formatted lines: object keys and block headers in
// blue, quoted values in green, number, boolean, and null values in white.
// Heredoc content and comments stay plain. It keeps every newline byte.
func colorizeLines(text string, m *lineMap, p palette) string {
	if p.key == "" {
		return text
	}
	endNL := strings.HasSuffix(text, "\n")
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for i, raw := range lines {
		lines[i] = colorizeLine(raw, m.heredoc[i+1], p)
	}
	text = strings.Join(lines, "\n")
	if endNL {
		text += "\n"
	}
	return text
}

// colorizeLine paints one formatted line. Heredoc lines pass untouched.
func colorizeLine(line string, heredoc bool, p palette) string {
	if heredoc || line == "" {
		return line
	}
	lead := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	t := line[len(lead):]
	if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//") ||
		t == "}" || strings.HasPrefix(t, "}") {
		return line
	}
	// Attribute: key = value. TrimRight removes the alignment padding.
	if k, v, ok := strings.Cut(t, "="); ok {
		key := strings.TrimRight(k, " \t")
		pad := k[len(key):]
		val := strings.TrimLeft(v, " \t")
		return lead + p.wrap(p.key, key) + pad + "=" + " " + colorizeValue(val, p)
	}
	// Block header: kind "label"... {.
	if strings.HasSuffix(t, "{") {
		return lead + colorizeHeader(strings.TrimSpace(strings.TrimSuffix(t, "{")), p) + " {"
	}
	return line
}

// colorizeValue paints an attribute value: quoted strings and references
// like var.location in green, numbers, booleans, and null in white. Any
// other value stays plain. References print as strings in query output, so
// they take the same color here.
var valueRef = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)

func colorizeValue(v string, p palette) string {
	if len(v) > 0 && v[0] == '"' {
		return p.wrap(p.str, v)
	}
	if v == "true" || v == "false" || v == "null" {
		return p.wrap(p.scalar, v)
	}
	if _, err := strconv.ParseFloat(v, 64); err == nil {
		return p.wrap(p.scalar, v)
	}
	if valueRef.MatchString(v) {
		return p.wrap(p.str, v)
	}
	return v
}

// colorizeHeader paints a block header: the identifier and each quoted
// label in blue, like an object key.
func colorizeHeader(header string, p palette) string {
	fields := strings.Fields(header)
	for i, f := range fields {
		if len(f) > 0 && f[0] == '"' {
			fields[i] = p.wrap(p.key, f)
		}
	}
	if len(fields) > 0 {
		fields[0] = p.wrap(p.key, fields[0])
	}
	return strings.Join(fields, " ")
}

// lineMap marks, per one-based source line, heredoc content lines.
type lineMap struct {
	heredoc map[int]bool // content lines of heredoc strings
}

// analyzeHCL parses src and computes the line map.
func analyzeHCL(src []byte, filename string) (*lineMap, error) {
	f, diags := hclsyntax.ParseConfig(src, filename, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, diags
	}
	m := &lineMap{heredoc: make(map[int]bool)}

	// Heredoc lines hold string data, so they pass colorize untouched.
	_ = hclsyntax.VisitAll(f.Body.(*hclsyntax.Body), func(n hclsyntax.Node) hcl.Diagnostics {
		te, ok := n.(*hclsyntax.TemplateExpr)
		if !ok {
			return nil
		}
		r := te.SrcRange
		if r.Start.Byte+1 < len(src) && src[r.Start.Byte] == '<' && src[r.Start.Byte+1] == '<' {
			for ln := r.Start.Line + 1; ln < r.End.Line; ln++ {
				m.heredoc[ln] = true
			}
		}
		return nil
	})
	return m, nil
}
