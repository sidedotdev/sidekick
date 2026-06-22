#!/bin/sh
# Temporary stress runner to surface flaky test failures.
# Runs the target test many times under -race and tees output.
set -u
cd "$(dirname "$0")/.."
go test ./scripts/ -run TestRestartProcessClearsLogsAndShowsStoppingStatus -count=200 -race 2>&1 | tee scripts/BEFORE.txt