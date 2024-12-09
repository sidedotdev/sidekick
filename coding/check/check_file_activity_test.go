package check

import (
	"os"
	"sidekick/env"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCheckFileActivity_invalidGoFile(t *testing.T) {
	testDir, filePath, err := writeTempFile(t, "go", "package main\n\nfunc main() {\n\tfmt.Println(\"Hello, World!\"\n}")
	if err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	defer os.RemoveAll(testDir)

	envContainer := env.EnvContainer{
		Env: &env.LocalEnv{
			WorkingDirectory: testDir,
		},
	}

	input := CheckFileActivityInput{
		FilePath:     filePath,
		EnvContainer: envContainer,
	}

	output, err := CheckFileActivity(input)
	assert.NoError(t, err)
	if output.Output == "" {
		t.Fatalf("expected output NOT to be empty, but it was")
	}
	if output.AllPassed {
		t.Fatalf("expected some checks to fail")
	}
}

func TestCheckFileActivity_invalidVueFile(t *testing.T) {
	testDir, filePath, err := writeTempFile(t, "vue", `<template>unclosed template`)
	if err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	defer os.RemoveAll(testDir)

	envContainer := env.EnvContainer{
		Env: &env.LocalEnv{
			WorkingDirectory: testDir,
		},
	}

	input := CheckFileActivityInput{
		FilePath:     filePath,
		EnvContainer: envContainer,
	}

	output, err := CheckFileActivity(input)
	assert.NoError(t, err)
	if output.Output == "" {
		t.Fatalf("expected output NOT to be empty, but it was")
	}
	if output.AllPassed {
		t.Fatalf("expected some checks to fail")
	}
}

func TestCheckFileActivity_validGoFile(t *testing.T) {
	testDir, filePath, err := writeTempFile(t, "go", "package main\n\nfunc main() {\n\tprintln(\"Hello, world!\")\n}")
	if err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	defer os.RemoveAll(testDir)

	envContainer := env.EnvContainer{
		Env: &env.LocalEnv{
			WorkingDirectory: testDir,
		},
	}

	input := CheckFileActivityInput{
		FilePath:     filePath,
		EnvContainer: envContainer,
	}

	output, err := CheckFileActivity(input)
	assert.NoError(t, err)
	assert.True(t, output.AllPassed)
	assert.Empty(t, output.Output)
}
