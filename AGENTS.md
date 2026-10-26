# Agent notes

## Versioning and releases

- The current version lives in the `VERSION` file (semver, no `v` prefix).
- `build.sh` reads `VERSION` and injects it into every binary.
- After finishing a task that changes behavior: bump `VERSION`, run
  `./build.sh`, commit the change together with `VERSION`, and create an
  annotated tag `v<version>`.
- `./build.sh` rebuilds `dist/` for all six platforms: macOS, Linux, and
  Windows in amd64 and arm64.

## Uploads

- Never upload artifacts to Azure Storage (azcopy or similar) unless the
  user explicitly asks for the upload.

## Verification

- Before finishing: run `gofmt -l .`, `go vet ./...`, and
  `go test ./...`.
