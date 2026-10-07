package fflag

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var assignmentJournalMu sync.Mutex

type flagAssignment struct {
	EvaluatedAt  time.Time              `json:"evaluatedAt"`
	TargetingKey string                 `json:"targetingKey"`
	Attributes   map[string]interface{} `json:"attributes,omitempty"`
	Flag         string                 `json:"flag"`
	Variant      string                 `json:"variant"`
	Version      string                 `json:"version,omitempty"`
	Value        interface{}            `json:"value"`
}

func appendFlagAssignment(input EvaluateFlagsInput, name, variant, version string, value interface{}) error {
	state := os.Getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(state) {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("resolve flag journal directory: %w", err)
		}
		state = filepath.Join(home, ".local", "state")
	}
	data, err := json.Marshal(flagAssignment{
		EvaluatedAt:  time.Now().UTC(),
		TargetingKey: input.TargetingKey,
		Attributes:   input.Attributes,
		Flag:         name, Variant: variant, Version: version, Value: value,
	})
	if err != nil {
		return err
	}
	data = append(data, '\n')

	assignmentJournalMu.Lock()
	defer assignmentJournalMu.Unlock()
	dir := filepath.Join(state, "sidekick")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(dir, "flag-assignments.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
