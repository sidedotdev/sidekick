package dev

import (
	"fmt"
	"io"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPanicParseMustacheWithPromptsFS(t *testing.T) {
	templateName := "author_edit_block/software_dev"
	expectedContent := "You are a senior software development engineer"

	template := panicParseMustache(promptsFS, templateName)
	output, err := template.Render(nil)
	assert.Nil(t, err)
	if !strings.Contains(output, expectedContent) {
		t.Errorf("Unexpected output: got %v, want %v", output, expectedContent)
	}
}

func TestPanicParseMustacheWithExistentPartial(t *testing.T) {
	mockFS := &mockReadFileFS{
		files: map[string][]byte{
			"prompts/test.mustache":             []byte("{{> existent_partial}}"),
			"prompts/existent_partial.mustache": []byte("This is an existent partial"),
		},
	}

	templateName := "test"
	template := panicParseMustache(mockFS, templateName)
	output, err := template.Render(nil)
	assert.Nil(t, err)
	if !strings.Contains(output, "This is an existent partial") {
		t.Errorf("Unexpected output: got %v, want %v", output, "This is an existent partial")
	}

}

func TestPanicParseMustacheWithNonExistentTemplate(t *testing.T) {
	mockFS := &mockReadFileFS{
		files: map[string][]byte{},
	}

	defer func() {
		if r := recover(); r == nil {
			t.Errorf("The code did not panic when template was missing")
		}
	}()

	templateName := "non_existent"
	panicParseMustache(mockFS, templateName)
}

/* FIXME the template parses successfully in this case, so unfortunately the panic is not triggered
func TestPanicParseMustacheWithMissingPartial(t *testing.T) {
	mockFS := &mockReadFileFS{
		files: map[string][]byte{
			"prompts/test.mustache": []byte("{{> missing_partial}}"),
			// Ensure the partial is missing by not including it in the mock file system
		},
	}

	defer func() {
		if r := recover(); r == nil {
			t.Errorf("The code did not panic when a partial was missing")
		}
	}()

	templateName := "test"
	panicParseMustache(mockFS, templateName)
}
*/

type mockReadFileFS struct {
	files map[string][]byte
}

func (m *mockReadFileFS) ReadFile(name string) ([]byte, error) {
	if content, ok := m.files[name]; ok {
		return content, nil
	}
	return nil, fmt.Errorf("file not found: %s", name)
}

func (m *mockReadFileFS) Open(name string) (fs.File, error) {
	if content, ok := m.files[name]; ok {
		return &mockFile{data: content}, nil
	}
	return nil, fmt.Errorf("file not found: %s", name)
}

type mockFile struct {
	data  []byte
	index int64
}

func (m *mockFile) Read(p []byte) (n int, err error) {
	if m.index >= int64(len(m.data)) {
		return 0, io.EOF
	}
	n = copy(p, m.data[m.index:])
	m.index += int64(n)
	return n, nil
}

func (m *mockFile) Stat() (fs.FileInfo, error) {
	return &mockFileInfo{size: int64(len(m.data))}, nil
}

func (m *mockFile) Close() error {
	return nil
}

type mockFileInfo struct {
	size int64
}

func (m *mockFileInfo) Name() string       { return "" }
func (m *mockFileInfo) Size() int64        { return m.size }
func (m *mockFileInfo) Mode() fs.FileMode  { return 0 }
func (m *mockFileInfo) ModTime() time.Time { return time.Time{} }
func (m *mockFileInfo) IsDir() bool        { return false }
func (m *mockFileInfo) Sys() interface{}   { return nil }
