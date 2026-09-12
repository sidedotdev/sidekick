#!/usr/bin/env bash
# Scratch red-green check: breaks one runner behavior at a time and confirms
# scripts/android_phone_remote_e2e/run_test.sh catches it.
set -u

ORIG=scripts/android_phone_remote_e2e/run.sh
T="$(mktemp -d)"

python3 - "$ORIG" "$T" <<'PY'
import pathlib
import sys

orig, out = sys.argv[1], pathlib.Path(sys.argv[2])
src = pathlib.Path(orig).read_text()

variants = {
    "no-pgroup.sh": [
        ('  set -m\n  "$@" &\n  local pid=$!\n  set +m\n', '  "$@" &\n  local pid=$!\n'),
        ('kill -TERM -"$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true',
         'kill -TERM "$pid" 2>/dev/null || true'),
        ('kill -KILL -"$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true',
         'kill -KILL "$pid" 2>/dev/null || true'),
    ],
    "no-redact.sh": [
        ('  if [ -n "$TICKET" ] || [ -n "$TOKEN" ]; then', '  if false; then'),
    ],
    "no-cleanup.sh": [
        ('  if [ -n "$PAIRED_DEVICE_ID" ]; then', '  if false; then'),
    ],
}

for name, substitutions in variants.items():
    text = src
    for old, new in substitutions:
        assert old in text, (name, old)
        text = text.replace(old, new)
    path = out / name
    path.write_text(text)
    path.chmod(0o755)
print("variants written")
PY

for variant in no-pgroup no-redact no-cleanup; do
  echo "=== break: $variant ==="
  scripts/android_phone_remote_e2e/run_test.sh "$T/$variant.sh" 2>&1 |
    grep -E '^(FAIL:|all harness|[0-9]+ harness)'
done

rm -rf "$T"