
<h1>
  <img src="frontend/public/android-chrome-512x512.png" alt="Sidekick Logo" height="45" align="left">&nbsp;
  Sidekick
</h1>

<!-- TODO insert demo gif here -->

Sidekick is AI automation designed to support software engineers working on
real-world projects.

**Key features**:

1. **Agentic flows**: Sidekick automatically finds relevant code context, edits
   code, then runs tests and iterates to fix issues based on test feedback.
2. **Human-in-the-loop**: You guide Sidekick by approving requirements and
   development plans, and by answering questions or debugging when the LLM
   inevitably gets stuck. The LLM can continue after you unblock it.
3. **Bring your own keys**: Sidekick runs fully on your development machine and
   talks to your AI provider directly, using your own keys.

Sidekick will eventually work with all popular programming languages and
frameworks, but for now, it only supports:

- golang
- typescript
- python
- vue (with typescript)

We use Sidekick to build itself, thus golang/typescript/vue are the best
supported languages.

See [language and framework support](#language-and-framework-support) for more details.

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

## Dependencies 

1. [temporal cli](https://docs.temporal.io/cli#installation)
2. [redis](https://redis.io/docs/install/install-redis/)
3. [ripgrep](https://github.com/BurntSushi/ripgrep?tab=readme-ov-file#installation)

### Language-specific Dependencies

Note: All language-specific dependencies are also dev dependencies for language-specific tests.

#### golang

- [gopls](https://github.com/golang/tools/blob/master/gopls/README.md#installation)

## Configuration

### side.toml

The `side.toml` file contains repositiory-specific configuration.

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

## Language and Framework Support

Sidekick is designed to support any programming language through tree-sitter,
but makes use of customized tree-sitter queries to extract relevant code context
and create high-quality codebase summaries, which means that all languages are
not supported out of the box. In addition, Sidekick also acts as a client for
Language Server Protocol (LSP) servers, which also requires language-specific
integration, and sometimes framework-specific integration.

### Tier 1

The best supported languages/frameworks are in this tier. These not only have
the basic functionality described in lower tiers, but also have a dedicated
maintainer who dogfoods the language-specific implementation to ensure its
quality is maintained.

| Language/Framework | Maintainer |
| -------- | --------- |
| golang | [side.dev](https://side.dev) |

### Tier 2

These languages also have additional rigour to ensure better code generation, including:

1. An LSP server integration
2. Built-in checks for common errors LLMs make
3. Built-in autofix functionality

Note: it's possible to add checks/autofix to your specific configuration even for languages without built-in support. See [check_commands](#configuration) and [autofix_commands](#configuration) for more details.

| Language/Framework | LSP Server | Checks | Autofix |
| -------- | --------- | --------- | --------- |
| golang | [gopls](https://github.com/golang/tools/gopls) | ✓ | ✓ |

### Tier 3

These languages only have basic support to retrieve symbols/signatures and/or
produce a repo summary.

| Language/Framework | Symbols | Repo Summary |
| -------- | --------- | --------- |
| typescript | ✓ | ✓ |
| vue | ✓ | ✓ |
| python | ✓ | ✓ |

### Planned Support

These languages have no support for retrieving symbols/signatures or producing a
repo summary, and are largely not recommended to be used with Sidekick until they are
supported. But they are supported by tree-sitter, so can be added with some effort.

That said, note that Sidekick can actually edit any plaintext file, but will be
forced to read large chunks of the file if not the entire file in these
situations. It will also potentially have trouble locating which files to edit:
you'll have to tell Sidekick the file paths you want to edit in that case. But
it can make sense to use sidekick today with markdown or html/css files despite
it not being supported, depending on your use case.

Planned languages include:

<!-- TODO create issues for each of these -->

- javascript
- java
- kotlin
- markdown
- jsx / tsx
- svelte
- rust
- ruby
- php
- c++
- c#
- c
- html
- css

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## Updating Dependencies

To update the project dependencies and ensure the `go.sum` file is up-to-date, run the following command:
