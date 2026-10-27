package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const sampleTF = `resource "aws_instance" "web" {
  count = 2
  ami = "ami-123"
}
`

// writeFile creates one .tf file in dir and returns its path.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	return path
}

func TestRunPrintsPrettyJSON(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf", sampleTF)
	var out, errBuf bytes.Buffer
	code := run([]string{".resource", path}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatalf("output is not valid JSON: %v\noutput: %q", err, out.String())
	}
	if _, ok := v["aws_instance"]; !ok {
		t.Errorf("aws_instance key missing: %q", out.String())
	}
	if !strings.Contains(out.String(), "\n  \"aws_instance\"") {
		t.Errorf("output does not use two-space indent: %q", out.String())
	}
}

func TestRunPrintsCompactJSON(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf", "tags = {\n  name = \"web\"\n  env = \"dev\"\n}\n")
	var out, errBuf bytes.Buffer
	code := run([]string{"-c", ".tags", path}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	// -c prints one line of compact JSON, like jq -c.
	want := "{\"name\":\"web\",\"env\":\"dev\"}\n"
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
	if !json.Valid(out.Bytes()) {
		t.Errorf("stdout is not valid JSON: %q", out.String())
	}
}

func TestRunQueryReachesIntoObjectWithVars(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf",
		`output "mock" { value = { location = var.location, prefix = var.resource_prefix } }`+"\n")
	var out, errBuf bytes.Buffer
	code := run([]string{"-r", "output.mock.value.prefix", path}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	if out.String() != "var.resource_prefix\n" {
		t.Errorf("stdout = %q, want %q", out.String(), "var.resource_prefix\n")
	}
}

func TestRunNoHTMLEscape(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf", "note = \"a<b&c\"\n")
	var out, errBuf bytes.Buffer
	code := run([]string{"-c", ".note", path}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	if want := "\"a<b&c\"\n"; out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

func TestRunCompactJSONParses(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf",
		`output "mock" { value = { prefix = "p1", tls = true, ports = [80, 443] } }`+"\n")
	var out, errBuf bytes.Buffer
	code := run([]string{"-c", "output.mock.value", path}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	want := "{\"prefix\":\"p1\",\"tls\":true,\"ports\":[80,443]}\n"
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
	var v map[string]any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Errorf("compact output is not valid JSON: %v", err)
	}
}

func TestRunFlagsAnywhere(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf",
		`output "mock" { value = { prefix = "p1", ports = [80] } }`+"\n")
	// Flags after the expression, and several flags together.
	var out, errBuf bytes.Buffer
	code := run([]string{"output.mock.value.prefix", "-r", path}, &out, &errBuf)
	if code != 0 || out.String() != "p1\n" {
		t.Errorf("flag after expr: code = %d, stdout = %q, stderr = %q", code, out.String(), errBuf.String())
	}
	out.Reset()
	code = run([]string{"-c", "-r", "output.mock.value.ports", path}, &out, &errBuf)
	if code != 0 || out.String() != "[80]\n" {
		t.Errorf("-c -r array: code = %d, stdout = %q", code, out.String())
	}
	out.Reset()
	// No -e flag: a missing key gives no match, empty output, exit 0.
	code = run([]string{"-c", "-r", "output.mock.value.location", path}, &out, &errBuf)
	if code != 0 || out.Len() != 0 {
		t.Errorf("missing key: code = %d, stdout = %q", code, out.String())
	}
}

func TestRunUnknownFlag(t *testing.T) {
	var out, errBuf bytes.Buffer
	code := run([]string{"-x", ".a"}, &out, &errBuf)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.HasPrefix(errBuf.String(), "hq: unknown flag") {
		t.Errorf("stderr = %q, want unknown flag error", errBuf.String())
	}
}

func TestRunRawString(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf", sampleTF)
	var out, errBuf bytes.Buffer
	code := run([]string{"-r", ".resource.aws_instance.web.ami", path}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	if out.String() != "ami-123\n" {
		t.Errorf("output = %q, want %q", out.String(), "ami-123\n")
	}
}

func TestRunRawNumberAndBool(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf", "count = 2\nenabled = true\n")
	for _, tc := range []struct{ expr, want string }{
		{".count", "2\n"},
		{".enabled", "true\n"},
	} {
		var out, errBuf bytes.Buffer
		code := run([]string{"-r", tc.expr, path}, &out, &errBuf)
		if code != 0 {
			t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
		}
		if out.String() != tc.want {
			t.Errorf("expr %s: output = %q, want %q", tc.expr, out.String(), tc.want)
		}
	}
}

func TestRunMultipleMatches(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf", "ids = [\"a\", \"b\", \"c\"]\n")
	var out, errBuf bytes.Buffer
	code := run([]string{"-r", ".ids[]", path}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	if out.String() != "a\nb\nc\n" {
		t.Errorf("output = %q, want %q", out.String(), "a\nb\nc\n")
	}
}

func TestRunMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	a := writeFile(t, dir, "a.tf", "name = \"first\"\n")
	b := writeFile(t, dir, "b.tf", "name = \"second\"\n")
	var out, errBuf bytes.Buffer
	code := run([]string{"-r", ".name", a, b}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	if out.String() != "first\nsecond\n" {
		t.Errorf("output = %q, want %q", out.String(), "first\nsecond\n")
	}
}

func TestRunNoMatchExitCode(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf", sampleTF)
	var out, errBuf bytes.Buffer
	code := run([]string{"-e", ".missing", path}, &out, &errBuf)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

func TestRunNoMatchWithoutE(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf", sampleTF)
	var out, errBuf bytes.Buffer
	code := run([]string{".missing", path}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

func TestRunMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.tf")
	var out, errBuf bytes.Buffer
	code := run([]string{".name", missing}, &out, &errBuf)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.HasPrefix(errBuf.String(), "hq:") {
		t.Errorf("stderr = %q, want hq: prefix", errBuf.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

func TestRunBadQuery(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf", sampleTF)
	var out, errBuf bytes.Buffer
	code := run([]string{"..a", path}, &out, &errBuf)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.HasPrefix(errBuf.String(), "hq:") {
		t.Errorf("stderr = %q, want hq: prefix", errBuf.String())
	}
}

func TestRunMissingExpr(t *testing.T) {
	old := stdinStat
	stdinStat = func() (os.FileInfo, error) {
		return fakeFileInfo{os.ModeDevice | os.ModeCharDevice}, nil
	}
	defer func() { stdinStat = old }()
	var out, errBuf bytes.Buffer
	code := run(nil, &out, &errBuf)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errBuf.String(), "Usage:") {
		t.Errorf("stderr = %q, want usage text", errBuf.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

func TestRunVersion(t *testing.T) {
	for _, flag := range []string{"-v", "--version"} {
		var out, errBuf bytes.Buffer
		code := run([]string{flag}, &out, &errBuf)
		if code != 0 {
			t.Fatalf("%s: exit code = %d, stderr = %q", flag, code, errBuf.String())
		}
		if out.String() != "hq version 0.1.2\n" {
			t.Errorf("%s: stdout = %q, want %q", flag, out.String(), "hq version 0.1.2\n")
		}
	}
}

func TestRunLongFlags(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf", "name = \"n\"\n")
	// --compact and --raw behave like their short forms.
	var out, errBuf bytes.Buffer
	code := run([]string{"--compact", "--raw", ".name", path}, &out, &errBuf)
	if code != 0 || out.String() != "n\n" {
		t.Errorf("compact raw: code = %d, stdout = %q", code, out.String())
	}
	// --error exits 1 on no match.
	out.Reset()
	code = run([]string{"--error", ".missing", path}, &out, &errBuf)
	if code != 1 {
		t.Errorf("error: exit code = %d, want 1", code)
	}
	// --json with --compact prints one line of JSON for the whole doc.
	out.Reset()
	code = run([]string{"--json", "--compact", path}, &out, &errBuf)
	if code != 0 || out.String() != `{"name":"n"}`+"\n" {
		t.Errorf("json: code = %d, stdout = %q", code, out.String())
	}
	// --help prints the usage text and exits 0.
	out.Reset()
	code = run([]string{"--help"}, &out, &errBuf)
	if code != 0 || !strings.Contains(out.String(), "Usage:") {
		t.Errorf("help: code = %d, stdout = %q", code, out.String())
	}
}

func TestRunStdin(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf", "name = \"from-stdin\"\n")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	old := os.Stdin
	os.Stdin = f
	defer func() { os.Stdin = old }()
	var out, errBuf bytes.Buffer
	code := run([]string{"-r", ".name"}, &out, &errBuf)
	_ = f.Close()
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	if out.String() != "from-stdin\n" {
		t.Errorf("stdout = %q, want %q", out.String(), "from-stdin\n")
	}
}

// fakeFileInfo has only the mode that stdinIsPiped checks.
type fakeFileInfo struct{ mode os.FileMode }

func (f fakeFileInfo) Name() string       { return "stdin" }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }

// runWithStdin runs hq with a file as standard input, marked as piped.
func runWithStdin(path string, stdout, stderr io.Writer) int {
	oldStat := stdinStat
	stdinStat = func() (os.FileInfo, error) { return fakeFileInfo{os.ModeNamedPipe}, nil }
	defer func() { stdinStat = oldStat }()
	old := os.Stdin
	f, err := os.Open(path)
	if err != nil {
		return -1
	}
	os.Stdin = f
	defer func() { os.Stdin = old }()
	return run(nil, stdout, stderr)
}

func TestRunFormatStdin(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf", "a  =    1\nname=\"x\"\n")
	var out, errBuf bytes.Buffer
	code := runWithStdin(path, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	// hclwrite.Format matches terraform fmt: alignment on, no inserted
	// blank line between the two attributes.
	want := "a    = 1\nname = \"x\"\n"
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

func TestRunFormatKeepsBlankLines(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf",
		"b { x = 1 }\n\n\n\n\nb2 {\ny = 2\nc = 3\n}\n")
	var out, errBuf bytes.Buffer
	code := runWithStdin(path, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	// terraform fmt adds no blank line and removes none: the three blank
	// lines stay and the single-line block stays on one line.
	want := "b { x = 1 }\n\n\n\n\nb2 {\n  y = 2\n  c = 3\n}\n"
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

func TestRunFormatHeredocBlankKept(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf",
		"a = 1\nu = <<EOT\nline1\n\n\nline2\nEOT\nb = 2\n")
	var out, errBuf bytes.Buffer
	code := runWithStdin(path, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	want := "a = 1\nu = <<EOT\nline1\n\n\nline2\nEOT\nb = 2\n"
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

func TestRunFormatCommentAttached(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf",
		"a = 1\n# note for b\nb = 2\n")
	var out, errBuf bytes.Buffer
	code := runWithStdin(path, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	want := "a = 1\n# note for b\nb = 2\n"
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

func TestRunFormatStdinInvalid(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf", "a = =\n")
	var out, errBuf bytes.Buffer
	code := runWithStdin(path, &out, &errBuf)
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.HasPrefix(errBuf.String(), "hq:") {
		t.Errorf("stderr = %q, want hq: prefix", errBuf.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

func TestRunFormatExpandsInlineObjects(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf",
		`output "mock" { value = { location = var.location, prefix = var.p, tags = var.tags } }`+"\n")
	var out, errBuf bytes.Buffer
	code := runWithStdin(path, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	// A single-line block stays one line, like terraform fmt. An object
	// with properties becomes one line per property.
	want := "output \"mock\" { value = {\n" +
		"  location = var.location\n" +
		"  prefix   = var.p\n" +
		"  tags     = var.tags\n" +
		"} }\n"
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

func TestRunFormatKeepsTuplesAndCallsInline(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf",
		`locals { ports = join(",", [80, 443]) }`+"\n"+
			`msg = "a${map({ k = 1 })}b"`+"\n")
	var out, errBuf bytes.Buffer
	code := runWithStdin(path, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	want := "locals { ports = join(\",\", [80, 443]) }\nmsg = \"a${map({ k = 1 })}b\"\n"
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

func TestRunFormatExpandsRulesInArray(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf",
		"rules = [\n"+
			`  { name = "Atfinity", source_addresses = ["10.11.4.128/25"], destination_fqdns = ["atfinity.hydrus.intern"] },`+"\n"+
			`  { name = "Metaco", protocols = [{ port = 80, type = "Http" }, { port = 443, type = "Https" }] }`+"\n"+
			"]\n")
	var out, errBuf bytes.Buffer
	code := runWithStdin(path, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	want := "rules = [\n" +
		"  {\n" +
		"    name              = \"Atfinity\"\n" +
		"    source_addresses  = [\"10.11.4.128/25\"]\n" +
		"    destination_fqdns = [\"atfinity.hydrus.intern\"]\n" +
		"  },\n" +
		"  {\n" +
		"    name = \"Metaco\"\n" +
		"    protocols = [{\n" +
		"      port = 80\n" +
		`      type = "Http"` + "\n" +
		"      }, {\n" +
		"      port = 443\n" +
		`      type = "Https"` + "\n" +
		"    }]\n" +
		"  }\n" +
		"]\n"
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

func TestRunFormatHeredocObjectsKept(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf",
		"a = 1\nu = <<EOT\n{ name = \"fake\", x = 1 }\nEOT\nb = 2\n")
	var out, errBuf bytes.Buffer
	code := runWithStdin(path, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	// Heredoc content is data, not structure: it stays on one line.
	want := "a = 1\nu = <<EOT\n{ name = \"fake\", x = 1 }\nEOT\nb = 2\n"
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

func TestRunFormatExpandsCRLF(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf",
		`output "o" { v = { a = 1, b = 2 } }`+"\r\n")
	var out, errBuf bytes.Buffer
	code := runWithStdin(path, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	want := "output \"o\" { v = {\r\n  a = 1\r\n  b = 2\r\n} }\r\n"
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

func TestColorEnabledEnv(t *testing.T) {
	cases := []struct {
		name  string
		force colorForce
		env   map[string]string
		want  bool
	}{
		{"no color by default for a non-terminal", forceNone, nil, false},
		{"-C forces color even with NO_COLOR", forceColor, map[string]string{"NO_COLOR": "1"}, true},
		{"-M forces no color even with HQ_COLOR=1", forceMono, map[string]string{"HQ_COLOR": "1"}, false},
		{"HQ_COLOR=1 forces color", forceNone, map[string]string{"HQ_COLOR": "1"}, true},
		{"HQ_COLOR=1 beats NO_COLOR", forceNone, map[string]string{"HQ_COLOR": "1", "NO_COLOR": "1"}, true},
		{"HQ_COLOR=1 beats TERM=dumb", forceNone, map[string]string{"HQ_COLOR": "1", "TERM": "dumb"}, true},
		{"HQ_COLOR=0 disables", forceNone, map[string]string{"HQ_COLOR": "0"}, false},
		{"NO_COLOR disables", forceNone, map[string]string{"NO_COLOR": "1"}, false},
		{"CLICOLOR_FORCE=1 forces color", forceNone, map[string]string{"CLICOLOR_FORCE": "1"}, true},
		{"CLICOLOR=0 disables", forceNone, map[string]string{"CLICOLOR": "0"}, false},
		{"TERM=dumb disables", forceNone, map[string]string{"TERM": "dumb"}, false},
		{"empty TERM disables, like jq", forceNone, map[string]string{"TERM": ""}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset the ambient state so the subtest is hermetic.
			for _, k := range []string{"HQ_COLOR", "NO_COLOR", "CLICOLOR", "CLICOLOR_FORCE"} {
				t.Setenv(k, "")
			}
			t.Setenv("TERM", "xterm")
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			var buf bytes.Buffer
			if got := colorEnabled(&buf, tc.force); got != tc.want {
				t.Errorf("colorEnabled = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRunColorsBeatsJqSemantics(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf",
		`output "mock" { value = { prefix = "p1" } }`+"\n")
	// -M beats -C, like jq, when both flags are given.
	var out, errBuf bytes.Buffer
	code := run([]string{"-C", "-M", "output.mock.value", path}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	if strings.Contains(out.String(), "\x1b[") {
		t.Errorf("stdout = %q, want no color with -M after -C", out.String())
	}
}

func TestRunFormatStdinColored(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf",
		"ami = \"a-1\"\nloc = var.location\nname = <<EOT\nplain text\nEOT\nnum = 2\n")
	oldStat := stdinStat
	stdinStat = func() (os.FileInfo, error) { return fakeFileInfo{os.ModeNamedPipe}, nil }
	defer func() { stdinStat = oldStat }()
	old := os.Stdin
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	os.Stdin = f
	defer func() { os.Stdin = old }()
	var out, errBuf bytes.Buffer
	code := run([]string{"-C"}, &out, &errBuf)
	_ = f.Close()
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	s := out.String()
	// Keys blue, string and reference green, number white.
	for _, want := range []string{
		// hq aligns '=' to the widest key ("name"), so "loc" gets padding.
		"\x1b[1;34mami\x1b[0m  = \x1b[0;32m\"a-1\"\x1b[0m",
		"\x1b[1;34mloc\x1b[0m  = \x1b[0;32mvar.location\x1b[0m",
		"\x1b[1;34mnum\x1b[0m  = \x1b[0;97m2\x1b[0m",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("stdout = %q, missing %q", s, want)
		}
	}
	// Heredoc content stays plain.
	if !strings.Contains(s, "plain text\n") {
		t.Errorf("stdout = %q, heredoc content must stay plain", s)
	}
}

func TestRunFileArgWithoutExpr(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf",
		`output "mock" { value = "v" }`+"\n")
	var out, errBuf bytes.Buffer
	// A file as first argument is the input file, not an expression. The
	// default output is formatted HCL, like the pipe.
	code := run([]string{path}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	if got := out.String(); got != "output \"mock\" { value = \"v\" }\n" {
		t.Errorf("stdout = %q", got)
	}
	// -c prints one line of compact JSON.
	out.Reset()
	code = run([]string{"-c", path}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	if got := out.String(); got != `{"output":{"mock":{"value":"v"}}}`+"\n" {
		t.Errorf("stdout = %q", got)
	}
	// -j prints the whole tree as JSON.
	out.Reset()
	code = run([]string{"-j", path}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	if got := out.String(); !strings.Contains(got, `"mock": {`) {
		t.Errorf("stdout = %q", got)
	}
	// -j -c prints one line of compact JSON.
	out.Reset()
	code = run([]string{"-j", "-c", path}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	if got := out.String(); got != `{"output":{"mock":{"value":"v"}}}`+"\n" {
		t.Errorf("stdout = %q", got)
	}
}

func TestRunPipedNoExprJson(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf",
		`output "mock" { value = "v" }`+"\n")
	oldStat := stdinStat
	stdinStat = func() (os.FileInfo, error) { return fakeFileInfo{os.ModeNamedPipe}, nil }
	defer func() { stdinStat = oldStat }()
	old := os.Stdin
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer func() { _ = f.Close() }()
	os.Stdin = f
	defer func() { os.Stdin = old }()
	var out, errBuf bytes.Buffer
	code := run([]string{"-j", "-c"}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	if got := out.String(); got != `{"output":{"mock":{"value":"v"}}}`+"\n" {
		t.Errorf("stdout = %q", got)
	}
}

func TestRunColoredOutput(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf",
		`output "mock" { value = { prefix = "p1", ports = [80] } }`+"\n")
	t.Setenv("HQ_COLOR", "1")
	var out, errBuf bytes.Buffer
	code := run([]string{"-c", "output.mock.value", path}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	s := out.String()
	for _, code := range []string{"\x1b[1;34m", "\x1b[0;32m", "\x1b[0;97m", "\x1b[0m"} {
		if !strings.Contains(s, code) {
			t.Errorf("stdout = %q, missing color code %q", s, code)
		}
	}
}

func TestRunFormatCRLFKept(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf", "locals {\r\n  a = 1\r\n\r\n  b = 2\r\n}\r\n")
	var out, errBuf bytes.Buffer
	code := runWithStdin(path, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	want := "locals {\r\n  a = 1\r\n\r\n  b = 2\r\n}\r\n"
	if out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

func TestRunFormatIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf",
		"a  =    1\nrules = [{ name = \"A\", x = 1 }, { name = \"B\", y = 2 }]\n\n\nb {\n  y = 2\n      z   = \"s\"\n}\n")
	var out, errBuf bytes.Buffer
	code := runWithStdin(path, &out, &errBuf)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, errBuf.String())
	}
	first := out.String()
	out.Reset()
	runWithStdin(writeFile(t, dir, "pass2.tf", first), &out, &errBuf)
	if out.String() != first {
		t.Errorf("second pass (=%q) differs from first (=%q)", out.String(), first)
	}
}

func TestRunQueryTypeError(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "main.tf", "name = \"n\"\n")
	var out, errBuf bytes.Buffer
	code := run([]string{".name[0]", path}, &out, &errBuf)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.HasPrefix(errBuf.String(), "hq: query: can not index") {
		t.Errorf("stderr = %q, want an index type error", errBuf.String())
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want empty", out.String())
	}
}

func TestRunReadErrorPrintsNothing(t *testing.T) {
	dir := t.TempDir()
	good := writeFile(t, dir, "a.tf", "name = \"first\"\n")
	missing := filepath.Join(t.TempDir(), "absent.tf")
	var out, errBuf bytes.Buffer
	// Format mode: a good file followed by a missing one must print no
	// partial output for the good file.
	code := run([]string{good, missing}, &out, &errBuf)
	if code != 1 {
		t.Errorf("format mode: exit code = %d, want 1", code)
	}
	if out.Len() != 0 {
		t.Errorf("format mode: stdout = %q, want empty", out.String())
	}
	if !strings.Contains(errBuf.String(), "read") {
		t.Errorf("format mode: stderr = %q, want a read error", errBuf.String())
	}
}
