# Sidekick's Usage of Tree-sitter

Given a source file (or source code block) in a supported language, this package
can use tree-sitter to:

1. List signatures
2. List symbols
3. Retrieve a symbol's definition, given a symbol name
4. "Shrink" the code (replace it with signatures/symbols and remove comments)

## Signatures

Signatures are formatted to look as close to the source language as possible,
just omitting the body. They are intended to provide a quick overview of what
functions, methods, classes, structs, constants etc. exist in a file, with a
primary focus on code localization (i.e. finding code relevant to an arbitrary
text prompt). By default, they're expected to exclude private signatures (if
that concept is relevant to a language). They're also used to summarize code
when too long.

The signature listing queries live at `signature_queries/signature_<language>.scm`.

Signatures are also accompanied by "headers", which refer to package
declarations and imports at the top-level, or whatever the corresponding
language feature is. This ensures that LLMs have the context of what libraries
are being used, and also the context required to edit those statements if/when
necessary.

The header queries live at `header_queries/header_<language>.scm`.

## Symbols 

This package provides two related but distinct symbol processing capabilities:

### Symbol Listing

Symbol listing provides a curated subset of symbols that are most relevant for
navigation and high-level code understanding, given a source file path. This is
exposed through `GetFileSymbols` and related functions.

The symbol listing:

- Returns top-level or otherwise relevant symbols
- Focuses on public symbols by default vs private (if that's a relevant language
  feature)
- Prioritizes symbols that are most relevant for navigation or high-level code
  understanding
- Is optimized for code localization and navigation use cases

The symbol listing queries are the same as the signature listing queries, and
live at  `signature_queries/signature_<language>.scm`.

### Retrieving Symbol Definitions

Symbol definition retrieval, in contrast, supports ALL possible symbols that
could be referenced in the code. It requires providing a symbol name and file
path, and returns the entire symbol definition.
This is exposed through `GetSymbolDefinitions` and related functions.

The symbol definitions:

- Can find the definition of any possible symbol in a given source file
- Includes private/internal symbols
- Returns full symbol context (modifiers, annotations, body, etc)
- Capture nested and local symbols
- Support code editing

The symbol definition queries live at `symbol_definition_queries/<language>.scm.mustache`.

## Adding a new language

To add a new language for tree-sitter (ignoring extras like LSP servers etc),
you'll need to:

1. Create `<language>.go` and `<language>_test.go` files under `coding/tree_sitter`.
2. Adjust case statements throughout to include the new language. Try `rg -C5 case.*vue` to find all case statements.
3. Create queries to list signatures and symbols `signature_queries/signature_<language>.scm`.
4. Create queries to retrieve symbol definitions `symbol_definition_queries/<language>.scm.mustache`.
5. Create queries to list packages/imports etc in `header_queries/<language>.scm`.
6. Add a custom comment query to the `removeComments` function.

Using an existing language that is similar to your language as a template is
strongly advised. Also note, tree-sitter parsers often have test corpuses that
can be used to help you develop your queries and test cases. Finally, unless you
are an LLM, the tree-sitter playground can be very helpful to debug queries.

Also, it's important to try to cover all possible language constructs. Think through how signatures should be 

### Parser Bindings

If the language's parser is not included in github.com/smacker/go-tree-sitter,
but does have a tree-sitter parser, follow the example of how vue is
implemented, i.e. add bindings for the parser under
`coding/tree_sitter/language_bindings/<language>`, along with a `binding.go`
file declaring the package, which should also be named for the language, and a
`GetLanguage` function.