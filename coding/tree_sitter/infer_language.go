package tree_sitter

import (
	"errors"
	"fmt"
	"sidekick/coding/tree_sitter/language_bindings/vue"
	"sidekick/utils"
	"unsafe"

	tree_sitter_kotlin "github.com/tree-sitter-grammars/tree-sitter-kotlin/bindings/go"
	tree_sitter "github.com/tree-sitter/go-tree-sitter"
	tree_sitter_go "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tree_sitter_java "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tree_sitter_python "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tree_sitter_typescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

var ErrFailedInferLanguage = errors.New("failed to infer language")

func inferLanguageFromFilePath(filePath string) (string, *tree_sitter.Language, error) {
	// TODO implement for all languages we support
	languageName := utils.InferLanguageNameFromFilePath(filePath)
	switch languageName {
	case "golang":
		return "golang", tree_sitter.NewLanguage(tree_sitter_go.Language()), nil
	case "typescript":
		return "typescript", tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript()), nil
	case "tsx":
		return "tsx", tree_sitter.NewLanguage(tree_sitter_typescript.LanguageTSX()), nil
	case "vue":
		return "vue", tree_sitter.NewLanguage(unsafe.Pointer(vue.Language())), nil
	case "python":
		return "python", tree_sitter.NewLanguage(tree_sitter_python.Language()), nil
	case "java":
		return "java", tree_sitter.NewLanguage(tree_sitter_java.Language()), nil
	case "kotlin":
		return "kotlin", tree_sitter.NewLanguage(tree_sitter_kotlin.Language()), nil
	default:
		return "", nil, fmt.Errorf("%w: %s", ErrFailedInferLanguage, filePath)
	}
}
