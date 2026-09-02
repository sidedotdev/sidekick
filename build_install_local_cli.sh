#!/bin/sh
set -ex

cd frontend
bun ci
bun run build
cd ..

export VERSION=$(git rev-parse --short HEAD)
export CGO_ENABLED=1
export CGO_LDFLAGS="-L. libusearch_c.a -lstdc++ -lm"

CACHE_DIR="${SIDE_CACHE_HOME:-${XDG_CACHE_HOME:-$HOME/.cache}/sidekick}"

# The file list must match common/agent_binary.go's agentSourceFiles.
AGENT_HASH=$(cat cmd/side-agent/main.go sideagent/client.go sideagent/gc.go sideagent/protocol.go sideagent/server.go sideagent/sftp.go | shasum -a 256 | cut -c1-12)
AGENT_DIR="$CACHE_DIR/agent-binaries"
mkdir -p "$AGENT_DIR"

for pair in linux-amd64 linux-arm64 darwin-amd64 darwin-arm64; do
  TARGET_OS="${pair%%-*}"
  TARGET_ARCH="${pair##*-}"
  DEST="$AGENT_DIR/side-agent-${TARGET_OS}-${TARGET_ARCH}-${AGENT_HASH}"
  if [ ! -f "$DEST" ]; then
    CGO_ENABLED=0 GOOS=$TARGET_OS GOARCH=$TARGET_ARCH go build -ldflags="-s -w" -o "$DEST" ./cmd/side-agent
  fi
done

go build -ldflags="-X main.version=${VERSION} -X sidekick/common.agentSourceHashOverride=${AGENT_HASH}" -o side sidekick/cli
sudo mv side /usr/local/bin/side
