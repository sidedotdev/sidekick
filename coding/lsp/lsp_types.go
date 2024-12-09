package lsp

import (
	"encoding/json"
	"fmt"
)

// The structs below represent the data structures used in the LSP's initialize request and response
type InitializeParams struct {
	ProcessID             int                    `json:"processId"`
	RootURI               string                 `json:"rootUri"`
	Capabilities          ClientCapabilities     `json:"capabilities"`
	WorkspaceFolders      *[]WorkspaceFolder     `json:"workspaceFolders,omitempty"`
	InitializationOptions map[string]interface{} `json:"initializationOptions,omitempty"`
}

type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

type ReferenceParams struct {
	TextDocumentPositionParams
	Context ReferenceContext `json:"context"`
}

type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

type CodeAction struct {
	Title       string          `json:"title"`
	Kind        *CodeActionKind `json:"kind,omitempty"`
	Diagnostics *[]Diagnostic   `json:"diagnostics,omitempty"`
	IsPreferred *bool           `json:"isPreferred,omitempty"`
	Disabled    *DisabledAction `json:"disabled,omitempty"`
	Edit        *WorkspaceEdit  `json:"edit,omitempty"`
	Command     *Command        `json:"command,omitempty"`
	Data        *LSPAny         `json:"data,omitempty"`
}

type DisabledAction struct {
	Reason string `json:"reason"`
}

type OptionalVersionedTextDocumentIdentifier struct {
	TextDocumentIdentifier
	Version *int `json:"version,omitempty"`
}

type AnnotatedTextEdit struct {
	Range        Range  `json:"range"`
	NewText      string `json:"newText"`
	AnnotationID string `json:"annotationId"`
}

/*
Describes textual changes on a single text document. The text document is
referred to as a OptionalVersionedTextDocumentIdentifier to allow clients to
check the text document version before an edit is applied. A TextDocumentEdit
describes all changes on a version Si and after they are applied move the
document to version Si+1. So the creator of a TextDocumentEdit doesn’t need to
sort the array of edits or do any kind of ordering. However the edits must be
non overlapping.
*/
type TextDocumentEdit struct {
	TextDocument OptionalVersionedTextDocumentIdentifier `json:"textDocument"`
	// This can be either TextEdit or AnnotatedTextEdit, but we don't support
	// AnnotatedTextEdit: This is guarded by the client capability
	// `workspace.workspaceEdit.changeAnnotationSupport`
	Edits []TextEdit `json:"edits"`
}

// The edit should either provide changes or documentChanges. If the client can
// handle versioned document edits and if documentChanges are present, the
// latter are preferred over changes.
type WorkspaceEdit struct {
	/*
		Whether a client supports versioned document edits is expressed via
		`workspace.workspaceEdit.documentChanges` client capability.

		For now, resourceOperations are not supported by this client,
		and is not a client capability, so we'll present there are only
		TextDocumentEdits. This is controlled by the client capability
		`workspace.workspaceEdit.resourceOperations`
	*/
	DocumentChanges []TextDocumentEdit `json:"documentChanges,omitempty"`

	/*
		TODO later if any servers only support old changes and not documentChanges:
		If a client neither supports `documentChanges` nor
		`workspace.workspaceEdit.resourceOperations` then only plain `TextEdit`s
		using the `changes` property are supported.
	*/
	// Changes           map[string][]TextEdit       `json:"changes,omitempty"`
	ChangeAnnotations map[string]ChangeAnnotation `json:"changeAnnotations,omitempty"`
}

type TextEdit struct {
	// The range of the text document to be manipulated. To insert
	// text into a document create a range where start === end.
	Range Range `json:"range"`
	// The string to be inserted. For delete operations use an
	// empty string.
	NewText string `json:"newText"`
}

type CreateFileOptions struct {
	Overwrite      *bool `json:"overwrite,omitempty"`
	IgnoreIfExists *bool `json:"ignoreIfExists,omitempty"`
}

type CreateFile struct {
	Kind         string             `json:"kind"`
	URI          string             `json:"uri"`
	Options      *CreateFileOptions `json:"options,omitempty"`
	AnnotationID *string            `json:"annotationId,omitempty"`
}

type RenameFileOptions struct {
	Overwrite      *bool `json:"overwrite,omitempty"`
	IgnoreIfExists *bool `json:"ignoreIfExists,omitempty"`
}

type RenameFile struct {
	Kind         string             `json:"kind"`
	OldURI       string             `json:"oldUri"`
	NewURI       string             `json:"newUri"`
	Options      *RenameFileOptions `json:"options,omitempty"`
	AnnotationID *string            `json:"annotationId,omitempty"`
}

type DeleteFileOptions struct {
	Recursive         *bool `json:"recursive,omitempty"`
	IgnoreIfNotExists *bool `json:"ignoreIfNotExists,omitempty"`
}

type DeleteFile struct {
	Kind         string             `json:"kind"`
	URI          string             `json:"uri"`
	Options      *DeleteFileOptions `json:"options,omitempty"`
	AnnotationID *string            `json:"annotationId,omitempty"`
}

type ChangeAnnotation struct {
	Label             string  `json:"label"`
	NeedsConfirmation *bool   `json:"needsConfirmation,omitempty"`
	Description       *string `json:"description,omitempty"`
}

type Command struct {
	// TODO: Define the fields for Command
}

type LSPAny *interface{}

type CodeActionParams struct {
	// TODO: PartialResultParams
	// The document in which the command was invoked.
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	// The range for which the command was invoked.
	Range Range `json:"range"`
	// Context carrying additional information.
	Context CodeActionContext `json:"context"`
}

type Diagnostic struct {
	Range Range `json:"range"`
	//Severity           *DiagnosticSeverity       `json:"severity,omitempty"`
	Code *interface{} `json:"code,omitempty"`
	//CodeDescription    *CodeDescription                `json:"codeDescription,omitempty"`
	Source  *string `json:"source,omitempty"`
	Message string  `json:"message"`
	//Tags               *[]DiagnosticTag                `json:"tags,omitempty"`
	//RelatedInformation *[]DiagnosticRelatedInformation `json:"relatedInformation,omitempty"`
	Data *interface{} `json:"data,omitempty"`
}

type CodeActionContext struct {
	Diagnostics []Diagnostic     `json:"diagnostics"`
	Only        []CodeActionKind `json:"only,omitempty"`
	TriggerKind int              `json:"triggerKind,omitempty"`
}

type CodeActionTriggerKind int

const (
	// Automatic code action trigger.
	CodeActionTriggerKindAutomatic CodeActionTriggerKind = 1

	// Invoked code action trigger.
	CodeActionTriggerKindInvoked CodeActionTriggerKind = 2
)

type TextDocumentIdentifier struct {
	DocumentURI string `json:"uri"`
}

type TypeDefinition struct {
	LinkSupport bool `json:"linkSupport"`
}

type Implementation struct {
	TypeDefinition TypeDefinition `json:"typeDefinition"`
}

type ClientCapabilities struct {
	TextDocument   TextDocumentClientCapabilities `json:"textDocument"`
	Implementation Implementation                 `json:"implementation"`
}

type TextDocumentClientCapabilities struct {
	CodeAction CodeActionClientCapabilities `json:"codeAction,omitempty"`
}

// CodeActionKind represents the kind of a code action.
// Kinds are a hierarchical list of identifiers separated by `.`,
// e.g. `"refactor.extract.function"`.
type CodeActionKind string

// A set of predefined code action kinds.
const (
	// Empty kind.
	CodeActionKindEmpty CodeActionKind = ""

	// Base kind for quickfix actions: 'quickfix'.
	CodeActionKindQuickFix CodeActionKind = "quickfix"

	// Base kind for refactoring actions: 'refactor'.
	CodeActionKindRefactor CodeActionKind = "refactor"

	// Base kind for refactoring extraction actions: 'refactor.extract'.
	// Example extract actions:
	// - Extract method
	// - Extract function
	// - Extract variable
	// - Extract interface from class
	// - ...
	CodeActionKindRefactorExtract CodeActionKind = "refactor.extract"

	// Base kind for refactoring inline actions: 'refactor.inline'.
	// Example inline actions:
	// - Inline function
	// - Inline variable
	// - Inline constant
	// - ...
	CodeActionKindRefactorInline CodeActionKind = "refactor.inline"

	// Base kind for refactoring rewrite actions: 'refactor.rewrite'.
	// Example rewrite actions:
	// - Convert JavaScript function to class
	// - Add or remove parameter
	// - Encapsulate field
	// - Make method static
	// - Move method to base class
	// - ...
	CodeActionKindRefactorRewrite CodeActionKind = "refactor.rewrite"

	// Base kind for source actions: `source`.
	// Source code actions apply to the entire file.
	CodeActionKindSource CodeActionKind = "source"

	// Base kind for an organize imports source action: `source.organizeImports`.
	CodeActionKindSourceOrganizeImports CodeActionKind = "source.organizeImports"

	// Base kind for a 'fix all' source action: `source.fixAll`.
	// 'Fix all' actions automatically fix errors that have a clear fix that
	// do not require user input. They should not suppress errors or perform
	// unsafe fixes such as generating new types or classes.
	CodeActionKindSourceFixAll CodeActionKind = "source.fixAll"
)

type CodeActionLiteralSupport struct {
	CodeActionKind *CodeActionKindValueSet `json:"codeActionKind"`
}

type CodeActionKindValueSet struct {
	ValueSet []CodeActionKind `json:"valueSet"`
}

type CodeActionClientCapabilities struct {
	DynamicRegistration      *bool                     `json:"dynamicRegistration,omitempty"`
	CodeActionLiteralSupport *CodeActionLiteralSupport `json:"codeActionLiteralSupport,omitempty"`
	IsPreferredSupport       bool                      `json:"isPreferredSupport,omitempty"`
	DisabledSupport          bool                      `json:"disabledSupport,omitempty"`
	DataSupport              bool                      `json:"dataSupport,omitempty"`
	ResolveSupport           *struct {
		Properties []string `json:"properties"`
	} `json:"resolveSupport,omitempty"`
	HonorsChangeAnnotations bool `json:"honorsChangeAnnotations,omitempty"`
}

type WorkspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

type Request struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      int              `json:"id"`
	Method  string           `json:"method"`
	Params  InitializeParams `json:"params"`
}

type InitializeResponse struct {
	Capabilities ServerCapabilities `json:"capabilities"`
}

type ServerCapabilities struct {
	TextDocumentSync                 interface{}                      `json:"textDocumentSync,omitempty"`
	CompletionProvider               *CompletionOptions               `json:"completionProvider,omitempty"`
	HoverProvider                    bool                             `json:"hoverProvider,omitempty"`
	SignatureHelpProvider            *SignatureHelpOptions            `json:"signatureHelpProvider,omitempty"`
	DefinitionProvider               bool                             `json:"definitionProvider,omitempty"`
	TypeDefinitionProvider           bool                             `json:"typeDefinitionProvider,omitempty"`
	ImplementationProvider           bool                             `json:"implementationProvider,omitempty"`
	ReferencesProvider               bool                             `json:"referencesProvider,omitempty"`
	DocumentHighlightProvider        bool                             `json:"documentHighlightProvider,omitempty"`
	DocumentSymbolProvider           bool                             `json:"documentSymbolProvider,omitempty"`
	CodeActionProvider               interface{}                      `json:"codeActionProvider,omitempty"`
	CodeLensProvider                 *CodeLensOptions                 `json:"codeLensProvider,omitempty"`
	DocumentFormattingProvider       bool                             `json:"documentFormattingProvider,omitempty"`
	DocumentRangeFormattingProvider  bool                             `json:"documentRangeFormattingProvider,omitempty"`
	DocumentOnTypeFormattingProvider *DocumentOnTypeFormattingOptions `json:"documentOnTypeFormattingProvider,omitempty"`
	RenameProvider                   interface{}                      `json:"renameProvider,omitempty"`
	DocumentLinkProvider             *DocumentLinkOptions             `json:"documentLinkProvider,omitempty"`
	Workspace                        *WorkspaceSpecificCapabilties    `json:"workspace,omitempty"`
	SemanticTokensProvider           interface{}                      `json:"semanticTokensProvider,omitempty"`
}

type CompletionOptions struct {
	ResolveProvider   bool     `json:"resolveProvider,omitempty"`
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
}

type SignatureHelpOptions struct {
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
}

type CodeLensOptions struct {
	ResolveProvider bool `json:"resolveProvider,omitempty"`
}

type DocumentOnTypeFormattingOptions struct {
	FirstTriggerCharacter string   `json:"firstTriggerCharacter"`
	MoreTriggerCharacter  []string `json:"moreTriggerCharacter,omitempty"`
}

type DocumentLinkOptions struct {
	ResolveProvider bool `json:"resolveProvider,omitempty"`
}

type WorkspaceSpecificCapabilties struct {
	WorkspaceFolders struct {
		Supported           bool            `json:"supported"`
		ChangeNotifications ChangeNotifType `json:"changeNotifications,omitempty"`
	} `json:"workspaceFolders"`
}

type ChangeNotifType struct {
	BoolValue   bool
	StringValue string
	IsBool      bool
}

func (c *ChangeNotifType) UnmarshalJSON(data []byte) error {
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		c.BoolValue = b
		c.IsBool = true
		return nil
	}

	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		c.StringValue = s
		c.IsBool = false
		return nil
	}

	return fmt.Errorf("ChangeNotifications must be a string or boolean")
}

func (c ChangeNotifType) MarshalJSON() ([]byte, error) {
	if c.IsBool {
		return json.Marshal(c.BoolValue)
	}
	return json.Marshal(c.StringValue)
}

type Position struct {
	/**
	 * Line position in a document (zero-based).
	 */
	Line int `json:"line"`

	/**
	 * Character offset on a line in a document (zero-based). The meaning of this
	 * offset is determined by the negotiated `PositionEncodingKind`.
	 *
	 * If the character value is greater than the line length it defaults back
	 * to the line length.
	 */
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}
