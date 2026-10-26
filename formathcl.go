package main

import (
	"io"
	"os"
	"regexp"
	"sort"
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

// formatHCL validates the input as HCL, then prints it in the canonical
// layout: multi-line blocks and objects, one blank line between sibling
// items, no blank line runs, and a single newline at the end. Colors follow
// the palette: keys blue, strings green, numbers and booleans white.
func formatHCL(r io.Reader, w io.Writer, force colorForce) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	data = expandInlineBraces(data, "(stdin)")
	m, err := analyzeHCL(data, "(stdin)")
	if err != nil {
		return err
	}
	lines := canonicalLines(string(hclwrite.Format(data)), m)
	lines = colorizeLines(lines, m, paletteFor(w, force))
	_, err = io.WriteString(w, lines)
	return err
}

// colorizeLines paints formatted lines: object keys and block headers in
// blue, quoted values in green, number, boolean, and null values in white.
// Heredoc content and comments stay plain.
func colorizeLines(text string, m *lineMap, p palette) string {
	if p.key == "" {
		return text
	}
	lines := strings.Split(strings.TrimSuffix(text, "\n"), "\n")
	for i, raw := range lines {
		lines[i] = colorizeLine(raw, m.heredoc[i+1], p)
	}
	return strings.Join(lines, "\n") + "\n"
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

// braceOpen tracks one open brace pair that may need expansion.
type braceOpen struct {
	line       int   // line of the opening brace
	openEnd    int   // byte offset just after the opening brace
	interior   int   // non-trivia tokens between the braces
	commaStart []int // byte offsets of item commas inside this brace
}

// braceEdit is one change to the source: a newline at Pos, which first
// replaces Del bytes.
type braceEdit struct {
	pos int
	del int
}

// expandInlineBraces breaks '{ ... }' pairs that sit on one line into
// several lines: a newline after '{', after every item comma of that brace,
// and before the matching '}'. Strings and heredocs are skipped, and commas
// inside function calls and tuples do not count. Later steps fix the
// indentation.
func expandInlineBraces(src []byte, filename string) []byte {
	tokens, diags := hclsyntax.LexConfig(src, filename, hcl.InitialPos)
	if diags.HasErrors() {
		return src
	}
	var edits []braceEdit
	var stack []braceOpen
	strDepth, parenDepth, brackDepth := 0, 0, 0
	for _, t := range tokens {
		switch t.Type {
		case hclsyntax.TokenOQuote, hclsyntax.TokenOHeredoc:
			strDepth++
		case hclsyntax.TokenCQuote, hclsyntax.TokenCHeredoc:
			strDepth--
		case hclsyntax.TokenOParen:
			parenDepth++
		case hclsyntax.TokenCParen:
			parenDepth--
		case hclsyntax.TokenOBrack:
			brackDepth++
		case hclsyntax.TokenCBrack:
			brackDepth--
		case hclsyntax.TokenOBrace:
			stack = append(stack, braceOpen{
				line:    t.Range.Start.Line,
				openEnd: t.Range.End.Byte,
			})
		case hclsyntax.TokenCBrace:
			if len(stack) == 0 {
				continue
			}
			top := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if strDepth != 0 || parenDepth != 0 {
				continue
			}
			if top.line != t.Range.Start.Line || top.interior == 0 {
				continue
			}
			// Newline after '{', before '}', and after '}', so that
			// sibling items get a line break. Each item ',' becomes a
			// newline.
			edits = append(edits, braceEdit{pos: top.openEnd})
			edits = append(edits, braceEdit{pos: t.Range.Start.Byte})
			edits = append(edits, braceEdit{pos: t.Range.End.Byte})
			for _, c := range top.commaStart {
				edits = append(edits, braceEdit{pos: c, del: 1})
			}
		case hclsyntax.TokenComma:
			if len(stack) > 0 && strDepth == 0 && parenDepth == 0 && brackDepth == 0 {
				top := &stack[len(stack)-1]
				top.commaStart = append(top.commaStart, t.Range.Start.Byte)
			}
		default:
			if !isTriviaToken(t.Type) && len(stack) > 0 {
				stack[len(stack)-1].interior++
			}
		}
	}
	return splice(src, edits)
}

// isTriviaToken tells if a token holds no structure at the brace level.
func isTriviaToken(ty hclsyntax.TokenType) bool {
	switch ty {
	case hclsyntax.TokenNewline, hclsyntax.TokenComment:
		return true
	}
	return false
}

// splice applies the edits to src in order: it drops Del bytes at Pos and
// then writes a newline.
func splice(src []byte, edits []braceEdit) []byte {
	if len(edits) == 0 {
		return src
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].pos < edits[j].pos })
	out := make([]byte, 0, len(src)+len(edits))
	prev := 0
	for _, e := range edits {
		out = append(out, src[prev:e.pos]...)
		prev = e.pos + e.del
		if prev > 0 && prev <= len(src) && src[prev-1] == '\n' {
			continue // a newline is already here
		}
		out = append(out, '\n')
	}
	out = append(out, src[prev:]...)
	return out
}

// lineMap marks, per one-based source line, item starts and heredoc content.
type lineMap struct {
	itemStart map[int]bool // first line of each non-first body item
	firstItem map[int]bool // first line of the first item of each body
	heredoc   map[int]bool // content lines of heredoc strings
}

// analyzeHCL parses src and computes the line map.
func analyzeHCL(src []byte, filename string) (*lineMap, error) {
	f, diags := hclsyntax.ParseConfig(src, filename, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, diags
	}
	m := &lineMap{
		itemStart: make(map[int]bool),
		firstItem: make(map[int]bool),
		heredoc:   make(map[int]bool),
	}
	var walkBody func(b *hclsyntax.Body)
	walkBody = func(b *hclsyntax.Body) {
		starts := make([]int, 0, len(b.Attributes)+len(b.Blocks))
		for _, a := range b.Attributes {
			starts = append(starts, a.SrcRange.Start.Line)
		}
		for _, blk := range b.Blocks {
			starts = append(starts, blk.TypeRange.Start.Line)
			walkBody(blk.Body)
		}
		sort.Ints(starts)
		for i, ln := range starts {
			if i == 0 {
				m.firstItem[ln] = true
			} else {
				m.itemStart[ln] = true
			}
		}
	}
	walkBody(f.Body.(*hclsyntax.Body))

	// Heredoc content is string data, so blank lines inside it must stay.
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

// canonicalLines applies the blank-line rules to formatted text. The line
// count of hclwrite.Format output equals the line count of its input, so
// the line numbers of lineMap apply. shortcut: it does not track multiline
// quoted strings, update it only if you format files with blank-line
// interpolations inside quoted strings.
func canonicalLines(formatted string, m *lineMap) string {
	lines := strings.Split(strings.TrimSuffix(formatted, "\n"), "\n")
	out := make([]string, 0, len(lines))
	for i, raw := range lines {
		srcLine := i + 1
		var line string
		if m.heredoc[srcLine] {
			line = strings.TrimSuffix(raw, "\r") // keep all other bytes
		} else {
			line = strings.TrimRight(raw, " \t\r")
		}
		switch {
		case m.heredoc[srcLine]:
			out = append(out, line) // keep exact heredoc content
		case strings.HasPrefix(line, "}"):
			// No blank line before a closing brace.
			for len(out) > 0 && out[len(out)-1] == "" {
				out = out[:len(out)-1]
			}
			out = append(out, line)
		case line == "":
			if len(out) > 0 && out[len(out)-1] != "" {
				out = append(out, "") // collapse runs of blank lines
			}
		case m.itemStart[srcLine]:
			// One blank line before a sibling item. Keep comment lines
			// attached to the item they describe.
			j := len(out) - 1
			for j >= 0 && isHCLComment(out[j]) {
				j--
			}
			if j >= 0 && out[j] != "" {
				// No blank yet: insert one before the comment run.
				out = append(out, "")
				copy(out[j+2:], out[j+1:])
				out[j+1] = ""
			}
			out = append(out, line)
		default:
			out = append(out, line)
		}
	}
	// No blank lines at the end of the file.
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return strings.Join(out, "\n") + "\n"
}

// isHCLComment tells if a line holds only a comment.
func isHCLComment(line string) bool {
	t := strings.TrimLeft(line, " \t")
	return strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//")
}
