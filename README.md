
<h1>
  <img src="frontend/public/android-chrome-512x512.png" alt="Sidekick Logo" height="45" align="left">&nbsp;
  Sidekick
</h1>

<!-- TODO insert demo gif here -->

Sidekick is an AI automation tool designed to support software engineers working
on real-world projects.

**Key features**:

1. **Agentic flows**: Sidekick automatically finds relevant code context, edits
   code, then runs tests and iterates to fix issues based on test feedback.
2. **Human-in-the-loop**: You guide Sidekick by approving requirements and
   development plans, and by answering questions or debugging when the LLM
   inevitably gets stuck. The LLM can continue after you unblock it.
3. **Bring your own keys**: Sidekick runs fully on your development machine and
   talks to your AI provider directly, using your own keys.

We use Sidekick to build itself, so it is very well optimized for golang,
typescript and vue. Use Sidekick when developing with any of the following
programming languages/frameworks:

- golang
- typescript
- java
- kotlin
- vue (with typescript)
- python

Note: while Sidekick can view and edit any text, it works better for the
specific languages that it has been optimized to support well, providing more
precise context and language-specific automated lints that catch errors LLMs
tend to make. Sidekick will eventually support these features for all popular
programming languages and frameworks.

See [language and framework support](#language-and-framework-support) for more details.

## Quickstart

### 1. Install the side cli

Download the [latest release](https://github.com/org-sidedev/sidekick/releases).

Add it to your `$PATH` and tell macOS to allow running side:

```sh
chmod +x side_macos_arm64_vx.x.x
sudo mv side_macos_arm64_vx.x.x /usr/local/bin/side
xattr -d com.apple.quarantine `which side`
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

1. [git](https://git-scm.com/book/en/v2/Getting-Started-Installing-Git)
2. [ripgrep](https://github.com/BurntSushi/ripgrep?tab=readme-ov-file#installation)

### Language-specific Dependencies

Note: All language-specific dependencies are also dev dependencies for language-specific tests.

#### golang

- [gopls](https://github.com/golang/tools/blob/master/gopls/README.md#installation)

## Configuration

### AGENTS.md

Sidekick automatically loads repository-specific instructions from an `AGENTS.md`
file in your project root. These instructions are included in prompts to the LLM
when planning changes and editing code. Use this to provide general instructions
related to coding conventions, explanations of the project's structure, etc.

If `AGENTS.md` is not present, Sidekick will look for other common agent
instruction files such as `CLAUDE.md`, `GEMINI.md`, `.github/copilot-instructions.md`,
and others. See the full list [in the source code](dev/get_repo_config.go). If you have a file that is unsupported, you can configure it in `side.yml` [like so](#edit_code).

### side.yml

The `side.yml` file (or `side.yaml`) contains repository-specific configuration.

<!-- TODO /gen document check_commands, autofix_commands, mission etc -->

#### test_commands

The most important thing to configure properly in Sidekick is the
`test_commands` array, which defines the list of commands that Sidekick will use
to run tests in your codebase. Automatically running tests and using feedback
from them is one of the key ways Sidekick catches hallucinations and ensures
your code is working as expected.

Example:

```yaml
test_commands:
  - command: "go test -test.timeout 15s ./..."
  - working_dir: "frontend"
    command: "bun run type-check"
```

If you do not have great test coverage, try using Sidekick to add tests prior to
making changes through Sidekick (or really, any AI-powered code generation
tool).

Tests are run relatively often by Sidekick, so it's important to ensure that
your tests are fast and reliable. You'd configure Sidekick to run only faster
unit tests over an entire integration or e2e test suite, for example. It's also
helpful to configure the test command to provide shortened output - while
Sidekick will automatically summarize long test outputs too, that comes at the
cost of time, money and accuracy.

You can optionally configure `integration_test_commands` separately from
`test_commands` if you want Sidekick to run slower integration tests less
frequently than unit tests. Sidekick will run these tests only at the end of a
task, instead of within each step or iteration.

#### edit_code

If you do not use a standard `AGENTS.md` file, you can configure Sidekick to load
instructions from a different file:

```yaml
edit_code:
  hints_path: "docs/ai-instructions.md"
```

#### command_permissions

The `command_permissions` section controls which shell commands Sidekick can run automatically, which require user approval, and which are blocked entirely.

```yaml
command_permissions:
  auto_approve:
    - pattern: "go test"
    - pattern: "npm run lint"
  require_approval:
    - pattern: "git push"
  deny:
    - pattern: "rm -rf"
      message: "Recursive force delete is not allowed"
```

Patterns are matched as literal prefixes by default. If a pattern contains regex metacharacters (`\.*+?[](){}|^$`), it's compiled as a regex anchored at the start.

Sidekick includes sensible defaults: common read-only commands like `ls`, `cat`, `git status`, `go test`, etc. are auto-approved, while dangerous commands like `sudo`, `rm -rf /`, `chmod 777` are denied.

Permission configs are merged in order: base defaults → local config → repo config → workspace config. Use `reset_auto_approve: true` or `reset_require_approval: true` to replace (rather than append to) previous rules. Deny rules always accumulate.

#### agent_config

The `agent_config` section allows per-use-case configuration for agent loops.
Use `auto_iterations` to set the number of iterations the agent will run
automatically before requesting human feedback.

```yaml
agent_config:
  coding:
    auto_iterations: 10
  planning:
    auto_iterations: 5
```

Use case names include `requirements`, `planning`, and `coding`.

#### worktree_setup

The `worktree_setup` field allows you to specify a shell script that will be
executed when setting up a local git worktree environment. Worktrees are used to
let Sidekick work without polluting your main working directory, and allows
multiple tasks to be done in parallel with conflicts. This is useful for
performing additional setup steps that are required for your development
environment, such as installing project-specific dependencies.

### .sideignore

Use a `.sideignore` file to control which files Sidekick sees, independent of git. It follows `.gitignore` syntax and takes precedence over `.gitignore` and `.ignore` files. This is useful for ignoring files like third-party vendored libraries that are tracked in git.

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
| kotlin | ✓ | ✓ |
| java | ✓ | ✓ |
| python | ✓ | ✓ |
| typescript | ✓ | ✓ |
| vue | ✓ | ✓ |
| tsx | ✓ | ✓ |

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
- markdown
- jsx
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