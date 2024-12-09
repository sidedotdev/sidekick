package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// WriteTestTempFile writes a temporary file with the provided extension and content
func WriteTestTempFile(t *testing.T, extension, code string) (string, error) {
	testDir := t.TempDir()
	tmpfile, err := os.CreateTemp(testDir, fmt.Sprintf("test*.%s", extension))
	if err != nil {
		return "", err
	}

	if _, err := tmpfile.Write([]byte(code)); err != nil {
		return "", err
	}
	if err := tmpfile.Close(); err != nil {
		return "", err
	}
	return tmpfile.Name(), nil
}

// WriteTestTempFile writes a temporary file with the provided filename and content
func WriteTestFile(t *testing.T, dir, filename, code string) (string, error) {
	tmpfile, err := os.Create(filepath.Join(dir, filename))
	if err != nil {
		return "", err
	}

	if _, err := tmpfile.Write([]byte(code)); err != nil {
		return "", err
	}
	if err := tmpfile.Close(); err != nil {
		return "", err
	}
	return tmpfile.Name(), nil
}
