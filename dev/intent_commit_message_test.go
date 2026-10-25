package dev

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeIntentCommitSubject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		subject string
		want    string
		wantErr bool
	}{
		{name: "prefixed", subject: "intent: require profile re-resolution", want: "intent: require profile re-resolution"},
		{name: "missing prefix", subject: "require profile re-resolution", want: "intent: require profile re-resolution"},
		{name: "duplicated prefix", subject: "intent: Intent:intent: require profile re-resolution", want: "intent: require profile re-resolution"},
		{name: "extra lines dropped", subject: " intent: require profile re-resolution\n\nbody ", want: "intent: require profile re-resolution"},
		{name: "prefix only", subject: "intent:", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeIntentCommitSubject(tt.subject)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestIntentCommitFallbackMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		paths []string
		want  string
	}{
		{name: "no paths", want: "intent: update"},
		{name: "intent paths", paths: []string{"intent/flows.md", "intent/idd/idd.md"}, want: "intent: update flows.md, idd/idd.md"},
		{name: "paths outside intent kept", paths: []string{"docs/intent/notes.md"}, want: "intent: update docs/intent/notes.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, intentCommitFallbackMessage(tt.paths))
		})
	}
}
