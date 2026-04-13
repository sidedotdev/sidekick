package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProgressRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "progress.json")

	p := &orphanProgress{
		CompletedBatches: 3,
		BatchSize:        5000,
		TotalKeys:        15000,
	}
	if err := writeProgress(path, p); err != nil {
		t.Fatal(err)
	}

	got, err := readProgress(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.CompletedBatches != p.CompletedBatches || got.BatchSize != p.BatchSize || got.TotalKeys != p.TotalKeys {
		t.Errorf("got %+v, want %+v", got, p)
	}
}

func TestReadProgress_Missing(t *testing.T) {
	t.Parallel()
	got, err := readProgress(filepath.Join(t.TempDir(), "nonexistent.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("expected nil for missing file, got %+v", got)
	}
}

func TestReadProgress_Corrupt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := readProgress(path)
	if err == nil {
		t.Fatal("expected error for corrupt file")
	}
}

func TestProgressFilePath(t *testing.T) {
	t.Parallel()
	got := progressFilePath("codec_orphans.bin")
	want := "codec_orphans.bin.progress.json"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
