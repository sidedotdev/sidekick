# Build the side CLI and install it into /usr/local/bin. Only the host's
# side-agent is pre-built; pass extra os-arch targets (e.g. linux-amd64) for
# remote environments on other platforms.
install *targets:
    #!/usr/bin/env bash
    set -euxo pipefail

    (cd frontend && bun ci && bun run build)

    cache_dir="${SIDE_CACHE_HOME:-${XDG_CACHE_HOME:-$HOME/.cache}/sidekick}"

    # The file list must match common/agent_binary.go's agentSourceFiles.
    agent_hash="$(cat cmd/side-agent/main.go sideagent/client.go sideagent/gc.go sideagent/protocol.go sideagent/server.go sideagent/sftp.go | shasum -a 256 | cut -c1-12)"
    agent_dir="$cache_dir/agent-binaries"
    mkdir -p "$agent_dir"

    for pair in "$(go env GOOS)-$(go env GOARCH)" {{targets}}; do
        target_os="${pair%%-*}"
        target_arch="${pair##*-}"
        dest="$agent_dir/side-agent-${target_os}-${target_arch}-${agent_hash}"
        if [[ ! -f "$dest" ]]; then
            CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -ldflags="-s -w" -o "$dest" ./cmd/side-agent
        fi
    done

    version="$(git rev-parse --short HEAD)"
    CGO_ENABLED=1 CGO_LDFLAGS="-L. libusearch_c.a -lstdc++ -lm" \
        go build -ldflags="-X main.version=${version} -X sidekick/common.agentSourceHashOverride=${agent_hash}" -o side sidekick/cli
    sudo mv side /usr/local/bin/side

# Build, install, and launch on the most recently connected online phone.
phone:
    #!/usr/bin/env bash
    set -euo pipefail

    adb="${ADB:-adb}"
    if ! command -v "$adb" >/dev/null 2>&1; then
        for sdk in "${ANDROID_HOME:-}" "${ANDROID_SDK_ROOT:-}" "$HOME/Library/Android/sdk" "$HOME/Android/Sdk"; do
            if [[ -n "$sdk" && -x "$sdk/platform-tools/adb" ]]; then
                adb="$sdk/platform-tools/adb"
                break
            fi
        done
    fi
    if ! command -v "$adb" >/dev/null 2>&1; then
        echo "adb not found; see app/android/README.md for SDK setup." >&2
        exit 1
    fi

    # Transport IDs increase as connections register with the running ADB server.
    devices="$("$adb" devices -l)"
    target="$(printf '%s\n' "$devices" | awk '
        $2 == "device" && $1 !~ /^emulator-/ {
            for (i = 3; i <= NF; i++) {
                if ($i ~ /^transport_id:[0-9]+$/) {
                    split($i, parts, ":")
                    if (parts[2] + 0 > newest) {
                        newest = parts[2] + 0
                        serial = $1
                    }
                }
            }
        }
        END { if (serial != "") printf "%s %s", newest, serial }
    ')"
    if [[ -z "$target" ]]; then
        echo "No online phone found. Connect a phone and authorize USB or wireless debugging (adb devices -l)." >&2
        exit 1
    fi
    read -r transport serial <<< "$target"
    printf 'Building for %s (ADB transport %s)\n' "$serial" "$transport"

    cd app/android
    ./gradlew :app:assembleDebug
    "$adb" -t "$transport" install -r app/build/outputs/apk/debug/app-debug.apk
    "$adb" -t "$transport" shell am start -W -n com.example.app/.MainActivity