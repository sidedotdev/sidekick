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
primary focus on code localization, and also being used to summarize code when
too long.

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

The symbol definition queries live at `symbol_definition_queries/*.scm.mustache`.