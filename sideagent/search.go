package sideagent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"sidekick/env/localio"
	"sidekick/repotools"
)

func (c *Client) BulkSearchRepository(ctx context.Context, directory string, params repotools.BulkSearchRepositoryParams) (string, error) {
	response, err := c.Exec(ctx, ExecRequest{
		Dir: directory, Search: &params, LoginEnv: true,
		TouchPath: "/tmp/.sidekick-activity",
	})
	if err != nil {
		return "", err
	}
	if response.Sealed {
		return "", fmt.Errorf("repository search refused: sandbox sealed")
	}
	if response.Error != "" || response.ExitStatus != 0 {
		return "", fmt.Errorf("repository search failed: %s (exit %d)", response.Error, response.ExitStatus)
	}
	var result string
	err = json.Unmarshal(response.Stdout, &result)
	return result, err
}

func (s *server) toolContext(id uint64) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		ticker := time.NewTicker(readLockPollInterval)
		defer ticker.Stop()
		for {
			if s.isCanceled(id) {
				cancel()
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-s.done:
				cancel()
				return
			case <-ticker.C:
			}
		}
	}()
	return ctx, cancel
}

func (s *server) executeSearch(req ExecRequest) ExecResponse {
	ctx, cancel := s.toolContext(req.ID)
	defer cancel()
	runner := repotools.RepositorySearchRunner{
		Context: ctx, Directory: req.Dir,
		Files: localio.FileSystem{WorkingDirectory: req.Dir},
		Command: func(command string, args ...string) (localio.RunCommandOutput, error) {
			if s.isCanceled(req.ID) {
				return localio.RunCommandOutput{}, context.Canceled
			}
			commandRequest := req
			commandRequest.Search = nil
			commandRequest.Argv = append([]string{command}, args...)
			response := s.executeCommand(commandRequest)
			output := localio.RunCommandOutput{
				Stdout: string(response.Stdout), Stderr: string(response.Stderr),
				ExitStatus: response.ExitStatus,
			}
			if response.Error != "" {
				return output, fmt.Errorf("%s", response.Error)
			}
			return output, nil
		},
	}
	result, err := repotools.BulkSearchRepository(runner, *req.Search)
	response := ExecResponse{ID: req.ID}
	if err == nil {
		response.Stdout, err = json.Marshal(result)
	}
	if err != nil {
		response.Error = err.Error()
		response.ExitStatus = -1
	}
	return response
}
