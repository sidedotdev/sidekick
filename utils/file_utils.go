package utils

import (
	"os"
	"path/filepath"
)

func FileExists(path string) bool {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false // The file does not exist
	}
	return err == nil // No error means the file exists
}

func InferLanguageNameFromFilePath(filePath string) string {
	// TODO implement for all languages we support
	ext := filepath.Ext(filePath)
	switch ext {
	case ".go":
		return "golang"
	case ".py":
		return "python"
	case ".ts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".vue":
		return "vue"
	case ".java":
		return "java"
	case ".kt":
		return "kotlin"
	case ".md":
		return "markdown"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".jsx":
		return "jsx"
	default:
		return "unknown"
	}
}
