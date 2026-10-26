#!/bin/sh
# Build hq for all release platforms into dist/.
# The version is read from VERSION and injected into the binary.
set -eu
cd "$(dirname "$0")"
export PATH="$HOME/.local/go/bin:$PATH"

VER=$(cat VERSION)
rm -f dist/hq-darwin-* dist/hq-linux-* dist/hq-windows-* dist/SHA256SUMS
mkdir -p dist

for p in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
	os=${p%/*}
	arch=${p#*/}
	ext=""
	[ "$os" = windows ] && ext=".exe"
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath \
		-ldflags "-s -w -X 'main.version=hq version $VER'" \
		-o "dist/hq-$os-$arch$ext" .
	echo "built dist/hq-$os-$arch$ext ($VER)"
done

cd dist
sha256sum hq-* > SHA256SUMS
cat SHA256SUMS
