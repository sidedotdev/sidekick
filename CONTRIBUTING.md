# Contributing to Sidekick

👋 Hello, potential contributor! We're thrilled that you're considering helping
out with Sidekick. Let's make something awesome together!

These guidelines are still a work-in-progress, but contain some essential
details to help you get started contributing more quickly.

## Table of Contents

<!-- TODO /gen/req fill these out
0. [Concepts](#concepts)
0. [Finding An Issue](#submitting-changes)
0. [Submitting Changes](#submitting-changes)
0. [Reporting Bugs](#reporting-bugs)
0. [Requesting Features](#requesting-features)
-->

1. [Development Setup](#development-setup)
2. [Development Workflow](#development-workflow)
3. [Style Guides](#style-guides)
4. [Miscellaneous](#miscellaneous)

## Development Setup

### Dev Dependencies

All the dependencies listed in README.md are required when developing the project. In addition, you will need:

1. go (min version 1.21.x): https://go.dev/doc/install
2. bun: https://bun.sh/
3. usearch: https://unum-cloud.github.io/usearch/golang/
4. [redis](https://redis.io/docs/install/install-redis/)

## Development Workflow

### Step 1: Start backend

Build the side cli and start all servers:

```sh
SIDE_LOG_LEVEL=0 SIDE_APP_ENV=development go run sidekick/cli start -- --disable-auto-open
```

### Step 2: Run the frontend

```sh
cd frontend
bun ci # just on first run
bun dev
```

### Step 3: Check out the web UI

Open http://localhost:5173/kanban

This assumes you have already run `side init` in at least one project.

### Step 4: Run Tests

```sh
go test -test.timeout 10s sidekick/... 
```

## Debugging

The best way to debug workflow logic is via replaying temporal event histories
for a flow where that bug was observed.

### Replays

A replay can be run locally like so (you'll have to have sidekick's temporal
server running at least):

```sh
go run sidekick/worker/replay -id $YOUR_FLOW_ID
```

You can utilize Printf debugging and keep replaying the same flow.

There is also a launch configuration for VSCode allowing you to debug a replayed
flow through dlv, with VSCode's UI. To do this, simply edit `.vscode/launch.json`
to put in the flow id, then run the "Replay Flow" launch configuration.

## Style Guides

### Golang Style Guide

- `go fmt` is supreme
- Casing:
  - Use camelCase for JSON struct tags in Go for all fields in structs that are
    used as JSON payloads.
  - `Id` is used instead of `ID` in all structs defined within sidekick

## Miscellaneous

### Build static binary

To get a (mostly - libc still required) static binary, we need a static version
of usearch first. First, clone usearch: 

```sh
git clone git@github.com:unum-cloud/usearch.git
cd usearch
```

Then build the usearch C static lib and move the static build file to the
sidekick root directory:

```sh
cmake -DUSEARCH_BUILD_TEST_CPP=1 -B build_release
cmake --build build_release --config Release
mv build_release/libusearch_static_c.a ../sidekick/libusearch_c.a 
```

Then back in sidekick's root directory, run build_install_local_cli.sh, which will build and install (into /usr/local/bin):

```sh
cd ../sidekick
./build_install_local_cli.sh
```

### Updating mocks

Before running these, take note of any manual modifications that may have been
necessary by searching for `NOTE` comments. While not great, it's necessary as
mockery gets confused by temporal's internal packages, which requires some
post-generation mock surgery.

```sh
mockery --srcpkg=go.temporal.io/sdk/client \
--name=Client \
--filename=temporal_client.go \
--output=mocks \
--outpkg=mocks

mockery --srcpkg=go.temporal.io/sdk/internal \
--name=ScheduleClient \
--filename=temporal_schedule_client.go \
--output=mocks \
--outpkg=mocks

mockery --srcpkg=go.temporal.io/sdk/internal \
--name=ScheduleHandle \
--filename=temporal_schedule_handle.go \
--output=mocks \
--outpkg=mocks
```

### Concepts

<!-- TODO /gen document the main concepts below -->

#### Workspace

#### Task

#### Flow

#### Subflow

#### Flow Action