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
// terraform fmt does: hclwrite.Format does the layout, and this function
// adds the palette colors. It injects no blank lines and removes none,
// so running it twice returns the input bytes.
func formatHCL(r io.Reader, w io.Writer, force colorForce) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	m, err := analyzeHCL(data, "(stdin)")
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, colorizeLines(string(hclwrite.Format(data)), m, paletteFor(w, force)))
	return err
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
