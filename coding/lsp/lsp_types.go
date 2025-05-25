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
	URI string `json:"uri"`
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
	Synchronization *TextDocumentSyncClientCapabilities `json:"synchronization,omitempty"`
	CodeAction      CodeActionClientCapabilities        `json:"codeAction,omitempty"`
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

// TextDocumentSyncKind defines how text documents are synced.
type TextDocumentSyncKind int

const (
	// Documents should not be synced at all.
	TextDocumentSyncKindNone TextDocumentSyncKind = 0

	// Documents are synced by always sending the full content
	// of the document.
	TextDocumentSyncKindFull TextDocumentSyncKind = 1

	// Documents are synced by sending the full content on open.
	// After that only incremental updates to the document are
	// sent.
	TextDocumentSyncKindIncremental TextDocumentSyncKind = 2
)

// SaveOptions represents options for save notifications.
type SaveOptions struct {
	/**
	 * The client is supposed to include the content on save.
	 */
	IncludeText *bool `json:"includeText,omitempty"`
}

// TextDocumentSyncOptions defines how text documents are synced.
type TextDocumentSyncOptions struct {
	/**
	 * Open and close notifications are sent to the server. If omitted open
	 * close notification should not be sent.
	 */
	OpenClose *bool `json:"openClose,omitempty"`

	/**
	 * Change notifications are sent to the server. See
	 * TextDocumentSyncKind.None, TextDocumentSyncKind.Full and
	 * TextDocumentSyncKind.Incremental. If omitted it defaults to
	 * TextDocumentSyncKind.None.
	 */
	Change *TextDocumentSyncKind `json:"change,omitempty"`

	/**
	 * If present will save notifications are sent to the server. If omitted
	 * the notification should not be sent.
	 */
	WillSave *bool `json:"willSave,omitempty"`

	/**
	 * If present will save wait until requests are sent to the server. If
	 * omitted the request should not be sent.
	 */
	WillSaveWaitUntil *bool `json:"willSaveWaitUntil,omitempty"`

	/**
	 * If present save notifications are sent to the server. If omitted the
	 * notification should not be sent.
	 */
	Save interface{} `json:"save,omitempty"` // Can be bool or SaveOptions
}

// TextDocumentSyncClientCapabilities represents client capabilities for text document synchronization.
type TextDocumentSyncClientCapabilities struct {
	/**
	 * Whether text document synchronization supports dynamic registration.
	 */
	DynamicRegistration *bool `json:"dynamicRegistration,omitempty"`

	/**
	 * The client supports sending will save notifications.
	 */
	WillSave *bool `json:"willSave,omitempty"`

	/**
	 * The client supports sending a will save request and
	 * waits for a response providing text edits which will
	 * be applied to the document before it is saved.
	 */
	WillSaveWaitUntil *bool `json:"willSaveWaitUntil,omitempty"`

	/**
	 * The client supports did save notifications.
	 */
	DidSave *bool `json:"didSave,omitempty"`
}

// TextDocumentItem represents an item to transfer a text document from the client to the server.
type TextDocumentItem struct {
	/**
	 * The text document's URI.
	 */
	URI string `json:"uri"`

	/**
	 * The text document's language identifier.
	 */
	LanguageID string `json:"languageId"`

	/**
	 * The version number of this document (it will increase after each
	 * change, including undo/redo).
	 */
	Version int `json:"version"`

	/**
	 * The content of the opened text document.
	 */
	Text string `json:"text"`
}

// VersionedTextDocumentIdentifier extends TextDocumentIdentifier with version information.
type VersionedTextDocumentIdentifier struct {
	TextDocumentIdentifier

	/**
	 * The version number of this document.
	 *
	 * The version number of a document will increase after each change,
	 * including undo/redo. The number doesn't need to be consecutive.
	 */
	Version int `json:"version"`
}

// DidOpenTextDocumentParams represents parameters for the textDocument/didOpen notification.
type DidOpenTextDocumentParams struct {
	/**
	 * The document that was opened.
	 */
	TextDocument TextDocumentItem `json:"textDocument"`
}

// TextDocumentContentChangeEvent represents an event describing a change to a text document.
type TextDocumentContentChangeEvent struct {
	/**
	 * The range of the document that changed.
	 */
	Range *Range `json:"range,omitempty"`

	/**
	 * The optional length of the range that got replaced.
	 *
	 * @deprecated use range instead.
	 */
	RangeLength *int `json:"rangeLength,omitempty"`

	/**
	 * The new text for the provided range or the new text of the whole document.
	 */
	Text string `json:"text"`
}

// DidChangeTextDocumentParams represents parameters for the textDocument/didChange notification.
type DidChangeTextDocumentParams struct {
	/**
	 * The document that did change. The version number points
	 * to the version after all provided content changes have
	 * been applied.
	 */
	TextDocument VersionedTextDocumentIdentifier `json:"textDocument"`

	/**
	 * The actual content changes. The content changes describe single state
	 * changes to the document. So if there are two content changes c1 (at
	 * array index 0) and c2 (at array index 1) for a document in state S then
	 * c1 moves the document from S to S' and c2 from S' to S''. So c1 is
	 * computed on the state S and c2 is computed on the state S'.
	 *
	 * To mirror the content of a document using change events use the following
	 * approach:
	 * - start with the same initial content
	 * - apply the 'textDocument/didChange' notifications in the order you
	 *   receive them.
	 * - apply the `TextDocumentContentChangeEvent`s in a single notification
	 *   in the order you receive them.
	 */
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

// DidSaveTextDocumentParams represents parameters for the textDocument/didSave notification.
type DidSaveTextDocumentParams struct {
	/**
	 * The document that was saved.
	 */
	TextDocument TextDocumentIdentifier `json:"textDocument"`

	/**
	 * Optional the content when saved. Depends on the includeText value
	 * when the save notification was requested.
	 */
	Text *string `json:"text,omitempty"`
}

// DidCloseTextDocumentParams represents parameters for the textDocument/didClose notification.
type DidCloseTextDocumentParams struct {
	/**
	 * The document that was closed.
	 */
	TextDocument TextDocumentIdentifier `json:"textDocument"`
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
