
<h1>
  <img src="frontend/public/android-chrome-512x512.png" alt="Sidekick Logo" height="45" align="left">
  Sidekick
</h1>


Sidekick is AI automation designed to support software engineers working on
real-world projects.

TODO: edits code, runs tests, fixes them, etc etc. genflow.dev content

## Demo

TODO

## Quickstart

Download the [latest release](https://github.com/org-sidedev/sidekick/releases).

Add it to your `$PATH`:

```sh
chmod +x side
mv side /usr/local/bin/
```

Then in your project's root directory:

```sh
side init
```

On first run, it will walk you through the setup process and ensure you've
installed the necessary [dependencies](#dependencies) and
[configured](#configuration) an AI provider.

## Configuration

### Workspace Configuration

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
