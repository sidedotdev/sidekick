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