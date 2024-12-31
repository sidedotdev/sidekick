package tree_sitter

import (
	"os"
	"path/filepath"
	"sidekick/utils"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetFileSymbolsStringVue(t *testing.T) {
	tests := []struct {
		name     string
		code     string
		expected string
	}{
		{
			name: "filename is included",
			code: `
				<template>
					<button @click="sayHello">Click me</button>
				</template>

				<script>
				export default {
					methods: {
						sayHello() {
							console.log('Hello, world!');
						}
					}
				}
				</script>
			`,
			expected: "<template>, <script>, sayHello, placeholder_filename",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tmpfile, err := os.CreateTemp("", "*.vue")
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(tmpfile.Name())
			sfcName := strings.ReplaceAll(filepath.Base(tmpfile.Name()), filepath.Ext(tmpfile.Name()), "")
			test.expected = strings.ReplaceAll(test.expected, "placeholder_filename", sfcName)

			if _, err := tmpfile.Write([]byte(test.code)); err != nil {
				t.Fatalf("Failed to write to temp file: %v", err)
			}
			if err := tmpfile.Close(); err != nil {
				t.Fatalf("Failed to close temp file: %v", err)
			}

			symbolsString, err := GetFileSymbolsString(tmpfile.Name())
			if err != nil {
				t.Fatalf("Failed to get symbols: %v", err)
			}

			if symbolsString != test.expected {
				t.Errorf("Got %s, expected %s", symbolsString, test.expected)
			}
		})
	}
}

func TestGetAllAlternativeFileSymbolsVue(t *testing.T) {
	tests := []struct {
		name                string
		code                string
		expectedSymbolNames []string
	}{
		{
			name: "filename is included",
			code: `
				<template>
					<button @click="sayHello">Click me</button>
				</template>

				<script>
				export default {
					methods: {
						sayHello() {
							console.log('Hello, world!');
						}
					}
				}
				</script>
			`,
			expectedSymbolNames: []string{"<template>", "<script>", "sayHello", "placeholder_filename"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Write the input to a temp file with a '.go' extension
			filePath, err := utils.WriteTestTempFile(t, "vue", tc.code)
			if err != nil {
				t.Fatalf("Failed to create temp file: %v", err)
			}
			defer os.Remove(filePath)

			for i, symbol := range tc.expectedSymbolNames {
				if symbol == "placeholder_filename" {
					tc.expectedSymbolNames[i] = strings.ReplaceAll(filepath.Base(filePath), filepath.Ext(filePath), "")
				}
			}

			// Call the function and check the output
			output, err := GetAllAlternativeFileSymbols(filePath)
			if err != nil {
				t.Fatalf("failed to get all alternative file symbols: %v", err)
			}
			outputStr := symbolToStringSlice(output)
			if !assert.ElementsMatch(t, outputStr, tc.expectedSymbolNames) {
				t.Errorf("Expected %s, but got %s", utils.PanicJSON(tc.expectedSymbolNames), utils.PanicJSON(outputStr))
			}
		})
	}
}
