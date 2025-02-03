package tree_sitter

import (
	"errors"
	"fmt"
	"sidekick/coding/tree_sitter/language_bindings/vue"
	"sidekick/utils"

	sitter "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/typescript/typescript"
	"github.com/smacker/go-tree-sitter/typescript/tsx"
)

var ErrFailedInferLanguage = errors.New("failed to infer language")

func inferLanguageFromFilePath(filePath string) (string, *sitter.Language, error) {
	// TODO implement for all languages we support
	languageName := utils.InferLanguageNameFromFilePath(filePath)
	switch languageName {
	case "golang":
		return "golang", golang.GetLanguage(), nil
	case "typescript":
		return "typescript", typescript.GetLanguage(), nil
	case "tsx":
		return "tsx", tsx.GetLanguage(), nil
	case "vue":
		return "vue", vue.GetLanguage(), nil
	case "python":
		return "python", python.GetLanguage(), nil
	case "java":
		return "java", java.GetLanguage(), nil
	default:
		return "", nil, fmt.Errorf("%w: %s", ErrFailedInferLanguage, filePath)
	}
}
