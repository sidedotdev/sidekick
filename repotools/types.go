package repotools

type FileSymDefRequest struct {
	FilePath string            `json:"file_path" jsonschema:"description=The path to a file\\, including relative path\\, eg: \"foo/bar/something.go\". This may be either the file where the symbol is defined OR a file that references/uses it; when a requested symbol is not defined in the given file\\, the tool automatically resolves the real definition via LSP go-to-definition (possibly in another repo file or a third-party library)."`
	Symbols  []RequestedSymbol `json:"symbols,omitempty" jsonschema:"description=List of symbols to retrieve from this file. Each entry should normally contain only the required \"name\" (the case-sensitive symbol name\\, eg a function\\, type\\, alias\\, interface\\, class\\, method\\, enum/member\\, constant\\, etc). If the list is empty\\, the entire file will be returned\\, but this usage is generally discouraged except for non-code files. Specifying the desired symbols is strongly recommended\\, even when all symbols are desired."`
}

// RequestedSymbol identifies one symbol to resolve within a FileSymDefRequest's
// file. ReferenceLine is consulted only by the LSP go-to-definition fallback to
// disambiguate when the same symbol text appears multiple times.
type RequestedSymbol struct {
	Name          string `json:"name" jsonschema:"description=Case-sensitive name of a code symbol defined or referenced in the file (eg \"someFunction\"\\, \"SomeType\"\\, \"SOME_CONSTANT\"). The full symbol definition will be returned (eg for a function\\, the entire function declaration including the body). When the symbol is only referenced (not defined) in file_path\\, the tool resolves the real definition via LSP and inlines it from wherever it lives (another repo file or a third-party library)."`
	ReferenceLine string `json:"reference_line,omitempty" jsonschema:"description=This field is only for disambiguation; leave it unset by default. When omitted\\, every occurrence of the symbol in file_path is resolved and the resulting definitions are de-duplicated. Set it only if multiple reference/usage occurrences could resolve to different definitions and you need to choose a particular occurrence. Do not use it when file_path contains the requested definition and do not copy a declaration such as \"func Something\" into it. The value must be a literal source line or distinctive substring containing the intended reference. If the provided snippet does not match any line in file_path\\, the tool returns an error listing the actual occurrence lines."`
}

type SymDefResults struct {
	SymbolDefinitions string
	Failures          string
}

type SingleSearchParams struct {
	PathGlob   string `json:"path_glob" jsonschema:"description=The file glob path to search within."`
	SearchTerm string `json:"search_term" jsonschema:"description=The search term to look for within the files."`
}

type BulkSearchRepositoryParams struct {
	ContextLines int                  `json:"context_lines" jsonschema:"description=The number of lines of context to include around the search term."`
	Searches     []SingleSearchParams `json:"searches" jsonschema:"description=The list of searches to perform."`
}
