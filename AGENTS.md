# Repository Guidelines

## Project Overview

`hq` is a single-binary Go CLI that parses HCL (HashiCorp Configuration
Language) files, such as Terraform `.tf` files. It is deterministic and
stateless: it reads only named files or standard input, writes results
to standard output, errors to standard error, and opens no network
connections. Two modes:

- **Query mode**: `hq [flags] 'expr' [file ...]` — jq/yq-style query,
  prints each match as JSON in source key order.
- **Format mode**: `hq [flags] [file ...]` — prints canonical HCL, like
  `terraform fmt`, or JSON with `-j`.

Exit codes: `0` success (also no matches without `-e`), `1` read/parse/
query error (also no matches with `-e`), `2` usage error.

## Architecture & Data Flow

`main.go::run(args, stdout, stderr) int` is the only entry point
(`main` is `os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))`).

1. **Flags**: jq-style toggles, valid in any position —
   `-c/--compact`, `-j/--json`, `-r/--raw`, `-e/--error`,
   `-C/--color-output`, `-M/--monochrome-output`, `-v/--version`,
   `-h/--help`.
2. **Argument resolution** (first rule that matches wins): no positional →
   read stdin (usage error on a terminal); first arg is `-` or an existing
   regular file → all positionals are files (format mode); otherwise the
   first arg is the expression (query mode).
3. **Parse**: `tree.Parse(data, filename)` / `tree.ParseFile` build an
   ordered `*tree.Node` from `hclsyntax`.
4. **Query mode**: `query.Eval(expr, root) ([]*tree.Node, error)`; each
   match prints via `printNode` as JSON (compact with `-c`). No match
   prints nothing. A segment that does not fit a node, like an index on
   a string, is an error, like jq.
5. **Format mode**: `renderWholeDoc` prints `formatHCL` output, or JSON
   via `renderJSON` (`-j`, `-c`); colors per `colors.go`. Layout:
   `hclwrite.Format` (the terraform fmt engine), then `expandInlineObjects`
   splits objects written on one line into one line per property, then a
   second `hclwrite.Format` aligns the new lines. Single-line blocks stay
   single-line; heredoc content and blank lines are untouched; idempotent.

Key model, `pkg/tree/node.go`: `Node{Kind, Bool, Num json.Number, Str,
Fields []Field, Items []*Node}` with Kinds `KindNull/KindBool/
KindNumber/KindString/KindObject/KindArray`. Objects keep insertion
order; `MarshalJSON` is hand-rolled (`writeJSON`) to preserve key order.
Block flattening in `pkg/tree/parse.go`: a block maps to the path
`blockType.label...`; repeated blocks with the same path fold into an
array in source order; an attribute and a block with the same name in
one body is a parse error. Expressions that reference variables or
functions can not evaluate; they fall back to the expression source text
as a string by design (hq never resolves Terraform variables).

## Key Directories

- `.` (main package) — CLI wiring, flags/argument resolution, HCL
  format mode (`formathcl.go`), ANSI palettes (`colors.go`).
- `pkg/tree` — HCL to ordered `Node` tree: `parse.go` (hclsyntax walk,
  block flattening, cty value extraction), `node.go` (model, ordered
  JSON).
- `pkg/query` — query expression parser and evaluator (`query.go`).
- `testdata/` — `sample.tf`, the shared test fixture.
- `pipelines/` — `ci.yml` (Azure Pipelines).
- `.github/` — GitHub Actions CI (`workflows/ci.yml`), release workflow
  for `v*` tags (`workflows/release.yml`), weekly dependency updates
  (`dependabot.yml`).
- `dist/` — gitignored release output: 6 binaries + `SHA256SUMS`.
- `.devcontainer/`, `.vscode/` — committed development environment.

## Development Commands

```sh
go build -o hq .                        # development build
go run . '.resource.aws_instance.web.ami' testdata/sample.tf
go test ./...                           # all tests
gofmt -l . && go vet ./...              # formatting and vet gate
./build.sh                              # release build, all 6 platforms
cd dist && sha256sum -c SHA256SUMS      # verify a release build
```

`build.sh` reads `VERSION`, cross-compiles
`darwin|linux|windows / amd64|arm64` with `CGO_ENABLED=0 -trimpath
-ldflags "-s -w -X 'main.version=hq version $VER'"` into `dist/`, and
writes `dist/SHA256SUMS`.

**CI** (`pipelines/ci.yml`, trigger `main`, `ubuntu-latest`): stage
`check` with parallel jobs — `tests` (`go test ./...`), `lint`
(golangci-lint v2.15.0), `security` (govulncheck v1.9.0, gosec v2.30.0,
gitleaks v8.30.1, all pinned) — then stage `build` runs `./build.sh`
and publishes `dist/` as artifact `hq-dist`.

GitHub Actions (`.github/workflows/ci.yml`) runs the same checks on
pushes to `main` and on pull requests; `workflows/release.yml` builds
and publishes release binaries on `v*` tags.

## Code Conventions & Common Patterns

- Go 1.27 (`go 1.27` in `go.mod`). Direct deps only
  `hashicorp/hcl/v2`, `mattn/go-isatty`, `zclconf/go-cty`; do not add
  more.
- Errors: CLI errors are `fmt.Errorf("hq: ...")` one-liners; parse
  errors come from `hcl.Diagnostics` (subject + detail, printed with
  `text/tabwriter`). Error strings carry no trailing period.
- Preserve source key order everywhere: `Node.Fields`, the JSON writer,
  and HCL rendering all follow it.
- Colors: palette in `colors.go`; on when stdout is a TTY (jq-like);
  env `HQ_COLOR`, `NO_COLOR`, `CLICOLOR_FORCE`, `CLICOLOR`, `TERM=dumb`;
  flags `-C`/`-M` override them.
- gosec convention: CLI file paths are operator-supplied, so
  `os.ReadFile`/`os.Stat` on them carry per-line `// #nosec G703,G304`.
  Never add global exclusions.
- Comments: one line, the why, or a known limit as
  `shortcut: <limit>, <when to upgrade>`. House style: STE — short
  active-voice sentences in comments and messages.

## Important Files

| File | Role |
|------|------|
| `main.go` | entry point: flags, argument resolution, run loop, `readInputs`, `printNode`, exit codes |
| `formathcl.go` | format mode: `hclwrite.Format` plus colorizing; heredoc lines stay plain, output is idempotent |
| `colors.go` | ANSI palette and TTY/env color decision |
| `pkg/tree/parse.go` | `hclsyntax` walk to `Node`, block flattening, cty value extraction with source-text fallback |
| `pkg/query/query.go` | grammar: `.a.b`, `a.b`, `["key"]`, `[i]` (negative ok), `[]`/`.*`/`*` splat, `\| keys`, `\| length`, `\| not` |
| `build.sh`, `VERSION` | release build and version injection (0.1.3) |
| `pipelines/ci.yml` | staged CI and the DevSecOps gate |
| `.devcontainer/devcontainer.json` | Go 1.27 image, gh/golangci-lint/gotestsum features, warms module cache |
| `README.md` | full user documentation: argument rules, output contract, query language, HCL-to-JSON mapping |

## Runtime/Tooling Preferences

- Go 1.27; user-local toolchain under `$HOME/.local/go`
  (`build.sh` prepends `$HOME/.local/go/bin` to `PATH`).
- No Docker needed to build or test. The devcontainer is optional:
  VS Code, image `mcr.microsoft.com/devcontainers/go:2-1.27-trixie`,
  non-root user `vscode`, extension `golang.Go`, default test task
  "hq: test" (`.vscode/tasks.json`).
- `dist/` and the local `hq` binary are gitignored (`.gitignore`);
  `.devcontainer/` and `.vscode/` stay committed and in sync.

## Testing & QA

- Plain stdlib `testing`, no external test deps. 106 tests in
  `main_test.go`, `pkg/tree/parse_test.go`,
  `pkg/query/query_test.go` + `fixture_test.go`.
- Table-driven. CLI tests call `run(args, &out, &err)` directly with
  `bytes.Buffer`s (no exec of the binary); fixtures come from
  `t.TempDir()` plus a small `writeFile` helper or `testdata/sample.tf`;
  stdin is simulated through a temp file (a `stdinStat` var keeps the
  `isRegularFile` check consistent); format tests round-trip
  (format, then re-parse).
- Gate, all must pass before finishing: `gofmt -l .` empty,
  `go vet ./...`, `go test ./...`, and the CI scanners — golangci-lint
  0 issues, govulncheck no reachable vulnerabilities, gosec clean
  (operator-supplied paths nosec-annotated), gitleaks no leaks.

## Versioning and Releases

- The current version lives in the `VERSION` file (semver, no `v`
  prefix; 0.1.3). `build.sh` reads `VERSION` and injects it into every
  binary.
- After finishing a task that changes user-visible behavior: bump
  `VERSION`, run `./build.sh`, and commit the change together with
  `VERSION`. Non-behavior changes (CI, lint, security dep bumps) keep
  the version.
- Do not push to GitHub and do not create the `v<version>` tag until
  the user asks for it explicitly. Every round, recompilation, and
  iteration may rebuild the binaries with `./build.sh` in `dist/`
  locally, but nothing is uploaded or published.
- `./build.sh` rebuilds `dist/` for all six platforms: macOS, Linux,
  and Windows in amd64 and arm64.

## Uploads

- Never upload artifacts to Azure Storage (azcopy or similar) unless
  the user explicitly asks for the upload.
