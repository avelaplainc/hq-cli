# hq — jq-style queries for HCL files

[![CI](https://github.com/avelaplainc/hq-cli/actions/workflows/ci.yml/badge.svg)](https://github.com/avelaplainc/hq-cli/actions/workflows/ci.yml)

## Purpose

`hq` is a single-binary command line tool that parses HCL (HashiCorp
Configuration Language) files, such as Terraform `.tf` files. It has two
modes:

- **Query mode**: extract values or structure from HCL and print the
  result as JSON. The query syntax follows `jq`/`yq`.
- **Format mode**: with no expression, print the input as canonical HCL,
  like `terraform fmt`, or as JSON with `-j`.

`hq` is deterministic and stateless. It reads only the named files or
standard input, writes results to standard output, and writes errors to
standard error. It starts no network connections.

Query results are JSON. Each match is one JSON document, so scripts and
AI agents can parse them directly. Format mode gives normalized HCL text
or a JSON rendering of the whole file.

## Install

Download a binary from the [releases page](https://github.com/avelaplainc/hq-cli/releases). One line, any platform — swap the OS and arch in the URL:

```sh
# Linux amd64
curl -fsSL https://github.com/avelaplainc/hq-cli/releases/latest/download/hq-linux-amd64 -o hq && chmod +x hq && sudo mv hq /usr/local/bin/

# Linux arm64
curl -fsSL https://github.com/avelaplainc/hq-cli/releases/latest/download/hq-linux-arm64 -o hq && chmod +x hq && sudo mv hq /usr/local/bin/

# macOS (arm64 Apple Silicon, use hq-darwin-amd64 on Intel)
curl -fsSL https://github.com/avelaplainc/hq-cli/releases/latest/download/hq-darwin-arm64 -o hq && chmod +x hq && sudo mv hq /usr/local/bin/
```

Windows (PowerShell):

```powershell
curl.exe -fsSL https://github.com/avelaplainc/hq-cli/releases/latest/download/hq-windows-amd64.exe -o hq.exe
```

Verify a download against `SHA256SUMS` from the same release:

```sh
sha256sum -c SHA256SUMS
```

Build from source instead:

```sh
go build -o hq .
```


Release build for all platforms:

```sh
./build.sh
```

`build.sh` produces one binary per target, macOS, Linux, and Windows in
amd64 and arm64, in `dist/`, plus `dist/SHA256SUMS`. The version is read
from the `VERSION` file and injected at build time; `-v` prints it.

Building requires Go 1.27 or later. The project depends on
`github.com/hashicorp/hcl/v2` for parsing.

## Usage

```
hq [flags] 'expr' [file ...]
hq [flags] [file ...]
```

### Argument resolution

Read these rules in order. The first rule that matches wins:

1. **No positional argument**: `hq` reads standard input when it is a
   pipe and formats it. With a terminal on standard input, `hq` prints
   the usage text and exits with code 2.
2. **First argument is `-` or an existing regular file**: there is no
   expression. Every positional argument is an input file, and `hq`
   prints each file formatted as HCL (or JSON with `-j`).
3. **Otherwise**: the first argument is the expression, and the remaining
   positional arguments are input files. With no file, `hq` reads one
   document from standard input.

Rules of note:

- Pass the expression as one quoted argument: `hq '.a.b' file.hcl`.
- Flags are toggles and may appear in any position, before or after the
  expression: `hq '.a' -c main.tf` and `hq -c '.a' main.tf` do the same.
- Multiple files: matches print in file order, one JSON document per
  match, with no separator.
- Use `-` as a file name to read standard input in any mode.

### Choosing a command

The sample file for this table appears in [Examples](#examples).

| Goal | Command |
|------|---------|
| One value, as JSON | `hq '.resource.aws_instance.web.ami' main.tf` |
| One scalar, as a plain string | `hq -r '.resource.aws_instance.web.ami' main.tf` |
| One subtree, compact, one line | `hq -c '.resource' main.tf` |
| Labels or keys at a level | `hq '.resource.aws_instance \| keys' main.tf` |
| Count items of a tuple or object | `hq '.resource.aws_instance.web.ports \| length' main.tf` |
| Same attribute of every block | `hq -r '.resource.aws_instance.*.instance_type' main.tf` |
| Whole file as pretty JSON | `hq -j main.tf` |
| Whole file as one line of JSON | `hq -j -c main.tf` |
| Whole file as formatted HCL | `hq main.tf` |
| Format piped input `cat main.tf \| hq` | |

### Flags

| Flag | Effect |
|------|--------|
| `-c`, `--compact` | Print the output on one line, as compact JSON. Same output for queries and whole documents. |
| `-j`, `--json` | When there is no expression, print as pretty JSON instead of formatted HCL. With an expression, it has no effect: query results are already JSON. |
| `-r`, `--raw` | Print scalar results raw (strings without quotes). Non-scalar results are unaffected. |
| `-e`, `--error` | Exit with code 1 when the query has no matches. |
| `-C`, `--color-output` | Force colors on. |
| `-M`, `--monochrome-output` | Force colors off. Wins over `-C`. |
| `-v`, `--version` | Print the version. |
| `-h`, `--help` | Print the usage text. |

### Output contract

**Query mode** (an expression is given):

- Every match prints as pretty JSON that keeps source key order. A
  subtree match can span several lines. `-c` prints each match as one
  line of compact JSON.
- String values are quoted, as JSON text. With `-r`, scalar results
  (string, number, boolean, null) print without quotes.
- A query with no matches prints nothing to standard output and exits
  with code 0. With `-e`, it exits with code 1.
- A query that does not fit the data is an error with exit code 1, like
  jq: an index on a string, a name on an array, an iteration over a
  scalar, or an unknown function after a pipe.

**Format mode** (no expression):

- Default: the formatted HCL document. `-j`: pretty JSON of the whole
  tree. `-c`: one line of compact JSON.
- The HCL layout is `hclwrite.Format`, the same engine terraform fmt
  uses:
  - two-space indent and `=` alignment;
  - blank lines are kept exactly as written: none are added, removed,
    or collapsed, and newlines at the end of the file stay;
  - CRLF line endings are kept, so formatting a CRLF file does not
    rewrite every line;
  - single-line blocks and objects stay single-line until the source
    itself breaks them;
  - heredoc content stays byte for byte;
  - formatting is idempotent: `hq` of `hq file` is `hq file`.

### Exit codes

| Code | Meaning |
|------|---------|
| `0` | Success. Also a query with no matches when `-e` is not set. |
| `1` | Read, parse, or query error. Also no matches when `-e` is set. |
| `2` | Usage error (unknown flag, terminal on standard input with no input). |

### For scripts and AI agents

Treat `hq` as a machine-readable data source:

- Standard output holds only results; errors go to standard error.
  Separate the streams before parsing.
- Parse the exit code. Non-zero never carries a usable result.
- Use `-c` for line-oriented pipelines: one JSON document per line.
- Use `-r` when a shell or tool needs the bare string, number, or
  boolean.
- Use `-e` to distinguish "key is absent" (exit 1, no output) from
  "file error" (exit 1, message on standard error).
- A result that is the source text of an expression (for example
  `"var.base_ami"` or `"aws_instance.web.ami"`) is an unevaluated
  reference, not a resolved value. hq does not resolve Terraform
  variables.
- The default output for a file argument or piped input is HCL, not
  JSON. Pass `-j` when you need JSON.

## Colors

Colors follow jq: they are on when standard output is a terminal with a
usable `TERM`, and `NO_COLOR` removes that default. Query results print
object keys in bold blue, strings in green, and numbers, booleans, and
null in bright white. The format mode uses the same palette: keys and
block headers blue, string values and references like `var.location`
green. `-r` output and errors are never colored.

Flag behavior:

| Flag | Effect |
|------|--------|
| `-C` | Force colors on; wins over `NO_COLOR` |
| `-M` | Force colors off; wins over `-C` |

Environment variables, when no flag is given:

| Variable | Effect |
|----------|--------|
| `HQ_COLOR=1` | Force colors on, even when piped |
| `HQ_COLOR=0` | Force colors off |
| `NO_COLOR` | Disable colors when set to any non-empty value ([no-color.org](https://no-color.org)) |
| `CLICOLOR_FORCE=1` | Force colors on |
| `CLICOLOR=0` | Disable colors |
| `TERM=dumb` or unset | Disable colors |

Without these variables, colors are on only for a terminal. Piped output
is plain by default.

## Query language

| Expression | Meaning |
|-------------|---------|
| `.a.b` | Object field access (HCL blocks nest by type and labels) |
| `a.b` | Same as `.a.b` |
| `["key.with.dots"]` | Bracket key access for keys with special characters |
| `[0]`, `[-1]` | Array index (negative counts from the end) |
| `[]`, `.*`, `*` | Splat: all array items, or all object field values |
| `\| length` | Number of object fields, array items, or 1 for scalars |
| `\| keys` | Array of the object field keys, in order |
| `\| not` | The jq truthiness inverse: `true` for `false` and `null`, else `false` |
| `\|` | Pipe: send the result to the next operation |

Semantics:

- A name that a node does not hold produces no result, not an error. An
  index out of range for an array is a no-match.
- A segment that does not fit a node is an error, like jq: an index on
  a string or an object, a name on an array, or an iteration over a
  scalar.
- After a pipe, a bare name must be one of the three functions above.
  Any other name is an error, not a silent no-match.
- An expression that does not parse is an error: a message on standard
  error and exit code 1.
- Repeated HCL blocks with the same type and labels fold into an array
  in source order. Example: `.resource.aws_instance` holds one entry per
  `resource "aws_instance" ...` block that shares its labels.

## HCL to JSON mapping

- Each body (file or block) maps to a JSON object.
- A block maps to nested objects: block type first, then each label.
- An attribute maps to a key with a value: numbers, strings, booleans,
  null, tuples and objects convert directly.
- Heredocs evaluate to plain strings.
- An expression that needs variables or functions (for example `var.ami`)
  can not be evaluated on its own. `hq` returns the expression source
  text as the string value. For a quoted template, like
  `"ami-${var.x}"`, that text is the template between the quote signs,
  `ami-${var.x}`, so JSON and `-r` output agree.
- Object constructor values (`tags = { name = "web" }`) keep source
  order, even when some items hold variables. Queries reach every item:
  `.output.mock.value.prefix` returns `var.resource_prefix`.

## Examples

Given this `main.tf`:

```hcl
terraform {
  required_version = ">= 1.5"
}

resource "aws_instance" "web" {
  ami           = "ami-123456"
  instance_type = "t3.micro"
  ports         = [80, 443]
}

resource "aws_instance" "db" {
  ami           = var.base_ami
  instance_type = "t3.small"
}
```

### Queries

```sh
$ hq '.resource.aws_instance.web.ami' main.tf
"ami-123456"

$ hq -r '.resource.aws_instance.web.ports[0]' main.tf
80

$ hq -r '.resource.aws_instance.*.instance_type' main.tf
t3.micro
t3.small

$ hq -c '.terraform' main.tf
{"required_version":">= 1.5"}

$ hq '.resource.aws_instance | keys' main.tf
["web","db"]

$ hq '.resource.aws_instance.web.ports | length' main.tf
2

$ hq '.resource.aws_instance.db.ami' main.tf
"var.base_ami"

$ hq '.missing' main.tf; echo $?

0

$ hq -e '.missing' main.tf; echo $?
1

$ echo 'region = "eu-west-1"' | hq '.region'
"eu-west-1"
```

### Format mode, whole file as JSON

```sh
$ hq main.tf
terraform {
  required_version = ">= 1.5"
}

resource "aws_instance" "web" {
  ami           = "ami-123456"
  instance_type = "t3.micro"
  ports         = [80, 443]
}

resource "aws_instance" "db" {
  ami           = var.base_ami
  instance_type = "t3.small"
}

$ hq -c main.tf
{"terraform":{"required_version":">= 1.5"},"resource":{"aws_instance":{"web":{"ami":"ami-123456","instance_type":"t3.micro","ports":[80,443]},"db":{"ami":"var.base_ami","instance_type":"t3.small"}}}}

$ hq -j main.tf
{
  "terraform": {
    "required_version": ">= 1.5"
  },
  "resource": {
    "aws_instance": {
      "web": {
        "ami": "ami-123456",
        "instance_type": "t3.micro",
        "ports": [
          80,
          443
        ]
      },
      "db": {
        "ami": "var.base_ami",
        "instance_type": "t3.small"
      }
    }
  }
}

$ hq -j -c main.tf
{"terraform":{"required_version":">= 1.5"},"resource":{"aws_instance":{"web":{"ami":"ami-123456","instance_type":"t3.micro","ports":[80,443]},"db":{"ami":"var.base_ami","instance_type":"t3.small"}}}}

$ cat main.tf | hq -j -
{
  "terraform": {
    "required_version": ">= 1.5"
  },
  ...
}
```

## Design

- `pkg/tree` parses HCL into a generic node tree (`hclsyntax.ParseConfig`)
  and renders it as ordered JSON or HCL.
- `pkg/query` is a hand-written lexer, parser and evaluator for the
  jq-style expression language.
- `main.go` glues both together behind a thin CLI (standard `flag`
  package only).

The expression model and CLI shape follow mikefarah/yq: an expression is
a pipe of operations over a context list of matched nodes.
