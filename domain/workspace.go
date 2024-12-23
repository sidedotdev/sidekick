package domain

import (
	"sidekick/common"
	"time"
)

// a workspace is the unit of organization for flows/tasks/etc and will be
// associated with specific users (which don't yet exist) and will have some
// top-level configuration, eg the repo directory
type Workspace struct {
	Id           string    `json:"id"`
	Name         string    `json:"name"`         // name of the workspace
	LocalRepoDir string    `json:"localRepoDir"` // local code repository directory
	Created      time.Time `json:"created"`      // creation timestamp of the workspace
	Updated      time.Time `json:"updated"`      // last update timestamp of the workspace
}

// TODO /gen move to workspace package, along with corresponding accessor
// methods to new Accessor type within workspace package, extracted from db
// package.
type WorkspaceConfig struct {
	LLM       common.LLMConfig       `json:"llm"`
	Embedding common.EmbeddingConfig `json:"embedding"`
}
