
<h1>
  <img src="frontend/public/android-chrome-512x512.png" alt="Sidekick Logo" height="45" align="left">
  Sidekick
</h1>


Sidekick is AI automation designed to support software engineers working on
real-world projects.

**Key features**:

1. **Agentic flows**: Sidekick automatically finds relevant code context, edits
   code, then runs tests and iterates to fix issues based on test feedback.
2. **Human-in-the-loop**: You guide Sidekick by approving requirements and
   development plans, and by answering questions or debugging when the LLM
   inevitably gets stuck.
3. **Bring your own keys**: The Sidekick server runs on your machine and talks
   to your AI provider directly, using your own keys.

## Demo

TODO

## Quickstart

### 1. Install the side cli

Download the [latest release](https://github.com/org-sidedev/sidekick/releases).

Add it to your `$PATH`:

```sh
chmod +x side_macos_arm64_x.x.x
sudo mv side_macos_arm64_x.x.x /usr/local/bin/side
```

### 2. Set up and configure a workspace

In your project's root directory, run:

```sh
side init
```

On first run, it will walk you through the setup process and ensure you've
installed the necessary [dependencies](#dependencies) and
[configured](#configuration) an AI provider and other settings.

### 3. Start the side server

Finally, start the side server/worker:

```sh
side start
```

Then you can create a task at http://localhost:8855/kanban

## Configuration

### side.toml

#### test_commands

The most important thing to configure properly in Sidekick is the
`test_commands` array, which defines the list of commands that Sidekick will use
to run tests in your codebase. Automatically running tests and using feedback
from them is one of the key ways we enable reduce hallucination and ensure your
code is working as expected.

If you do not have great test coverage, we suggest using Sidekick or other tools
to add tests prior to making changes through Sidekick (or really, any AI-powered
code generation tooling).

Tests are run relatively often by Sidekick, so it's important to ensure that
your tests are fast and reliable. It's also helpful to configure them to provide
shortened output, though Sidekick will automatically summarize long test outputs
too, at the cost of time and accuracy.

<!-- TODO /gen document check_commands, autofix_commands, mission etc -->

<!-- TODO /gen document LLM and Embedding configuration in the workspace config -->

### .sideignore

<!-- TODO /gen how and when to use the .sideignore file -->

## Dependencies 

1. [temporal cli](https://docs.temporal.io/cli#installation)
2. [redis](https://redis.io/docs/install/install-redis/)
3. [ripgrep](https://github.com/BurntSushi/ripgrep?tab=readme-ov-file#installation)

<!-- TODO /gen-->

### Language-specific Dependencies

Note: All language-specific dependencies are also dev dependencies for language-specific tests.

#### golang

- [gopls](https://github.com/golang/tools/blob/master/gopls/README.md#installation)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).
