package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"hq/pkg/query"
	"hq/pkg/tree"
)

// version is the default for development builds; build.sh injects the
// value from the VERSION file with -ldflags -X.
var version = "hq version 0.2.0"

func writeUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage: hq [flags] 'expr' [file ...]
       hq [flags] [file ...]

hq parses HCL (HashiCorp Configuration Language) files, such as
Terraform .tf files. With an expression, it prints query results as
JSON, like jq. Without an expression, it formats the input as
canonical HCL, like terraform fmt.

Arguments:
  expr      a jq-style query, e.g. .resource.aws_instance.web.ami
  file ...  input files. Use "-" or no file to read standard input.
            When the first argument names an existing file, it is the
            input file, not an expression.

Flags (toggles, valid in any position):
  -c, --compact           print the output on one line, as compact JSON
  -j, --json              when there is no expression, print as JSON
                          instead of formatted HCL
  -r, --raw               print scalar results raw, without quotes
  -e, --error             exit with code 1 when the query has no matches
  -C, --color-output      force colors on
  -M, --monochrome-output force colors off; wins over -C
  -v, --version           print the version
  -h, --help              print this usage

Output:
  With an expression, every match prints as pretty JSON that keeps
  source key order; a subtree can span several lines. -c prints each
  match as one line of compact JSON. A query with no matches prints
  nothing and exits 0.
  Without an expression, the output is the formatted HCL document,
  compact JSON with -c, or JSON with -j.
  Colors are on when standard output is a terminal, like jq; -C
  and -M override them.

Exit codes:
  0  success; also a query with no matches when -e is not set
  1  read, parse, or query error; also no matches when -e is set
  2  usage error

Examples:
  hq '.resource.aws_instance.web.ami' main.tf
  hq -r '.output.name.value' main.tf
  hq '.resource.aws_instance | keys' main.tf
  hq -c '.resource' main.tf
  hq main.tf
  hq -j -c main.tf
  cat main.tf | hq '.region'
`)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	// All flags are toggles, and they can appear in any position. Like jq,
	// -M beats -C when both are given.
	var compact, raw, failNoMatch, showVersion, jsonBool bool
	force := forceNone
	var rest []string
	for _, a := range args {
		switch a {
		case "-c", "--compact":
			compact = true
		case "-j", "--json":
			jsonBool = true
		case "-r", "--raw":
			raw = true
		case "-e", "--error":
			failNoMatch = true
		case "-C", "--color-output":
			force = forceColor
		case "-M", "--monochrome-output":
			force = forceMono
		case "-v", "--version":
			showVersion = true
		case "-h", "--help":
			writeUsage(stdout)
			return 0
		case "-":
			rest = append(rest, a)
		default:
			if strings.HasPrefix(a, "-") {
				_, _ = fmt.Fprintf(stderr, "hq: unknown flag %q\n", a)
				writeUsage(stderr)
				return 2
			}
			rest = append(rest, a)
		}
	}
	if showVersion {
		_, _ = fmt.Fprintln(stdout, version)
		return 0
	}
	if len(rest) == 0 {
		if !stdinIsPiped() {
			writeUsage(stderr)
			return 2
		}
		docs, err := readInputs([]string{"-"})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "hq: %v\n", err)
			return 1
		}
		if err := renderWholeDoc(docs[0].data, docs[0].name, compact, jsonBool, force, stdout); err != nil {
			_, _ = fmt.Fprintf(stderr, "hq: %v\n", err)
			return 1
		}
		return 0
	}
	// A first argument that names an existing file is the input file, not
	// an expression. The default output is formatted HCL, like the pipe.
	if rest[0] == "-" || isRegularFile(rest[0]) {
		// Read every input before printing anything, so a read error can
		// not leave partial output on stdout.
		docs, err := readInputs(rest)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "hq: %v\n", err)
			return 1
		}
		for _, doc := range docs {
			if err := renderWholeDoc(doc.data, doc.name, compact, jsonBool, force, stdout); err != nil {
				_, _ = fmt.Fprintf(stderr, "hq: %v\n", err)
				return 1
			}
		}
		return 0
	}
	expr := rest[0]
	files := rest[1:]
	if len(files) == 0 {
		files = []string{"-"}
	}
	// Read every input before printing anything, so a read error can
	// not leave partial output on stdout.
	docs, err := readInputs(files)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "hq: %v\n", err)
		return 1
	}
	total := 0
	for _, doc := range docs {
		root, err := tree.Parse(doc.data, doc.name)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "hq: %v\n", err)
			return 1
		}
		matches, err := query.Eval(expr, root)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "hq: %v\n", err)
			return 1
		}
		for _, m := range matches {
			if err := printNode(stdout, m, compact, raw, force); err != nil {
				_, _ = fmt.Fprintf(stderr, "hq: %v\n", err)
				return 1
			}
		}
		total += len(matches)
	}
	if failNoMatch && total == 0 {
		return 1
	}
	return 0
}

// isRegularFile tells if name names an existing regular file.
func isRegularFile(name string) bool {
	fi, err := os.Stat(name) // #nosec G703
	return err == nil && fi.Mode().IsRegular()
}

// inputDoc is one input document: its bytes and its display name.
type inputDoc struct {
	name string
	data []byte
}

// readInputs reads every named file, in order. A "-" reads standard input.
// It reads all inputs before any output, so a read error can not leave
// partial results on stdout.
func readInputs(names []string) ([]inputDoc, error) {
	docs := make([]inputDoc, 0, len(names))
	for _, name := range names {
		if name == "-" {
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return nil, fmt.Errorf("read stdin: %v", err)
			}
			docs = append(docs, inputDoc{name: "(stdin)", data: data})
			continue
		}
		data, err := os.ReadFile(name) // #nosec G703,G304
		if err != nil {
			return nil, fmt.Errorf("read %s: %v", name, err)
		}
		docs = append(docs, inputDoc{name: name, data: data})
	}
	return docs, nil
}

// renderWholeDoc prints one parsed document without a query: formatted
// HCL by default, JSON with -j, one line of compact JSON with -c.
func renderWholeDoc(data []byte, name string, compact, jsonBool bool, force colorForce, stdout io.Writer) error {
	if !compact && !jsonBool {
		return formatHCL(bytes.NewReader(data), stdout, force)
	}
	root, err := tree.Parse(data, name)
	if err != nil {
		return err
	}
	p := paletteFor(stdout, force)
	var line string
	if compact {
		line = renderJSONCompact(root, p) + "\n"
	} else {
		line = renderJSON(root, p)
	}
	_, err = io.WriteString(stdout, line)
	return err
}

func printNode(w io.Writer, n *tree.Node, compact, raw bool, force colorForce) error {
	if raw {
		if s, ok := rawScalar(n); ok {
			_, err := fmt.Fprintln(w, s)
			return err
		}
	}
	p := paletteFor(w, force)
	var line string
	if compact {
		// One line of compact JSON, like jq -c.
		line = renderJSONCompact(n, p) + "\n"
	} else {
		// Pretty JSON with two-space indent.
		line = renderJSON(n, p)
	}
	_, err := io.WriteString(w, line)
	return err
}

// rawScalar returns the plain text form for scalar nodes.
func rawScalar(n *tree.Node) (string, bool) {
	switch n.Kind {
	case tree.KindString:
		return n.Str, true
	case tree.KindBool:
		return fmt.Sprintf("%t", n.Bool), true
	case tree.KindNumber:
		return n.Num.String(), true
	case tree.KindNull:
		return "null", true
	}
	return "", false
}
