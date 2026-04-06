#!/bin/sh
set -ex

cd frontend
bun ci
bun run build
cd ..

export VERSION=$(git rev-parse --short HEAD)
export CGO_ENABLED=1
export CGO_LDFLAGS="-L. libusearch_c.a -lstdc++ -lm"

# Compute walker source hash and pre-build binaries for common targets
WALKER_HASH=$(shasum -a 256 cmd/side-walker/main.go | cut -c1-12)
CACHE_DIR="${SIDE_CACHE_HOME:-${XDG_CACHE_HOME:-$HOME/.cache}/sidekick}"
WALKER_DIR="$CACHE_DIR/walker-binaries"
mkdir -p "$WALKER_DIR"

for pair in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64; do
  TARGET_OS="${pair%%-*}"
  TARGET_ARCH="${pair##*-}"
  DEST="$WALKER_DIR/side-walker-${TARGET_OS}-${TARGET_ARCH}-${WALKER_HASH}"
  if [ ! -f "$DEST" ]; then
    CGO_ENABLED=0 GOOS=$TARGET_OS GOARCH=$TARGET_ARCH go build -ldflags="-s -w" -o "$DEST" ./cmd/side-walker
  fi
done

go build -ldflags="-X main.version=${VERSION} -X sidekick/common.walkerSourceHashOverride=${WALKER_HASH}" -o side sidekick/cli
sudo mv side /usr/local/bin/side
