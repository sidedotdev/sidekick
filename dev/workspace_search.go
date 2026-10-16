package dev

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"sidekick/env"
	"sidekick/sideagent"

	"golang.org/x/sync/errgroup"
)

type workspaceExecutor interface {
	ExecuteWorkspace(context.Context, sideagent.WorkspaceRequest) (sideagent.WorkspaceResponse, error)
}

// Setup is scoped to one tool call, so ignore-file changes are visible on the
// next call without repeating filesystem RPCs for every search or retry.
type bulkActivitySearchRunner struct {
	activitySearchRunner
	envContainer env.EnvContainer
	setupOnce    sync.Once
	setupOutput  EnsureCoreIgnoreFileActivityOutput
	setupErr     error
	prepared     *sideagent.WorkspaceSearchState
}

func (r *bulkActivitySearchRunner) ensureCoreIgnoreFile(input EnsureCoreIgnoreFileActivityInput) (EnsureCoreIgnoreFileActivityOutput, error) {
	r.setupOnce.Do(func() {
		if executor, ok := r.envContainer.Env.(workspaceExecutor); ok {
			var result sideagent.WorkspaceResponse
			result, r.setupErr = executor.ExecuteWorkspace(r.ctx, sideagent.WorkspaceRequest{
				PrepareSearch: &sideagent.WorkspaceSearchSetup{
					IgnorePath: coreIgnoreRelPath, IgnoreContent: coreIgnoreFileContent,
				},
			})
			r.prepared = result.PreparedSearch
			if r.setupErr == nil && r.prepared == nil {
				r.setupErr = fmt.Errorf("workspace search setup returned no state")
			}
			r.setupOutput.Path = filepath.Join(r.envContainer.Env.GetWorkingDirectory(), coreIgnoreRelPath)
		} else {
			r.setupOutput, r.setupErr = r.activitySearchRunner.ensureCoreIgnoreFile(input)
		}
	})
	return r.setupOutput, r.setupErr
}

func (r *bulkActivitySearchRunner) runCommand(input env.EnvRunCommandActivityInput) (env.EnvRunCommandOutput, error) {
	if input.Command == "cat" && len(input.Args) == 1 && (input.Args[0] == ".sideignore" || input.Args[0] == ".gitignore") {
		if _, err := r.ensureCoreIgnoreFile(EnsureCoreIgnoreFileActivityInput{EnvContainer: r.envContainer}); err != nil {
			return env.EnvRunCommandOutput{}, err
		}
		if r.prepared != nil {
			exists := r.prepared.SideIgnoreExists
			if input.Args[0] == ".gitignore" {
				exists = r.prepared.GitIgnoreExists
			}
			if exists {
				return env.EnvRunCommandOutput{}, nil
			}
			return env.EnvRunCommandOutput{ExitStatus: 1}, nil
		}
	}
	return r.activitySearchRunner.runCommand(input)
}

func (r *bulkActivitySearchRunner) searchWorkspace(plan sideagent.WorkspaceSearch) (*sideagent.WorkspaceSearchResult, bool, error) {
	executor, ok := r.envContainer.Env.(workspaceExecutor)
	if !ok {
		return nil, false, nil
	}
	result, err := executor.ExecuteWorkspace(r.ctx, sideagent.WorkspaceRequest{Search: &plan})
	if err == nil && result.Fallback {
		return nil, false, nil
	}
	if err == nil && result.Search == nil {
		err = fmt.Errorf("workspace search returned no result")
	}
	return result.Search, true, err
}

func bulkSearchRepositoryInActivity(ctx context.Context, container env.EnvContainer, params BulkSearchRepositoryParams) (string, error) {
	group, ctx := errgroup.WithContext(ctx)
	group.SetLimit(8)
	runner := &bulkActivitySearchRunner{
		activitySearchRunner: activitySearchRunner{ctx: ctx},
		envContainer:         container,
	}
	results := make([]string, len(params.Searches))
	for i, search := range params.Searches {
		group.Go(func() error {
			result, err := bulkSearchRepository(runner, container, BulkSearchRepositoryParams{
				ContextLines: params.ContextLines,
				Searches:     []SingleSearchParams{search},
			})
			results[i] = result
			return err
		})
	}
	if err := group.Wait(); err != nil {
		return "", err
	}
	return strings.Join(results, "\n"), nil
}
