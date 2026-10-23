package sideagent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"sidekick/coding/lsp/core"
	"sidekick/env/localio"
	"sidekick/repotools"
)

// BulkGetSymbolDefinitions runs the get_symbol_definitions tool inside the
// sandbox, where file reads and language-server traffic are local.
func (c *Client) BulkGetSymbolDefinitions(ctx context.Context, directory string, request repotools.DirectorySymDefRequest) (repotools.SymDefResults, error) {
	response, err := c.Exec(ctx, ExecRequest{
		Dir: directory, SymbolDefinitions: &request, LoginEnv: true,
		TouchPath: "/tmp/.sidekick-activity",
	})
	if err != nil {
		return repotools.SymDefResults{}, err
	}
	if response.Sealed {
		return repotools.SymDefResults{}, fmt.Errorf("symbol definitions refused: sandbox sealed")
	}
	if response.Error != "" || response.ExitStatus != 0 {
		return repotools.SymDefResults{}, fmt.Errorf("symbol definitions failed: %s (exit %d)", response.Error, response.ExitStatus)
	}
	var results repotools.SymDefResults
	err = json.Unmarshal(response.Stdout, &results)
	return results, err
}

// languageServers owns the sandbox-local language-server sessions, which
// outlive individual requests so repeated lookups reuse a warm gopls.
type languageServers struct {
	once     sync.Once
	sessions *core.Sessions
}

func (l *languageServers) get(environ []string) *core.Sessions {
	l.once.Do(func() {
		l.sessions = &core.Sessions{
			LSPClientProvider:  core.NewLocalServerProvider(environ),
			InitializedClients: make(map[string]core.LSPClient),
		}
	})
	return l.sessions
}

func (l *languageServers) close() {
	if l.sessions != nil {
		_ = l.sessions.Close()
	}
}

func (s *server) executeSymbolDefinitions(req ExecRequest) ExecResponse {
	ctx, cancel := s.toolContext(req.ID)
	defer cancel()
	tool := repotools.SymbolDefinitions{
		Repository: repotools.LocalRepository{
			FileSystem:      localio.FileSystem{WorkingDirectory: req.Dir},
			IgnoreFileNames: repotools.IgnoreFileNames,
		},
		Sessions: s.languageServers.get(append(s.baseEnv(req.LoginEnv), req.Env...)),
		Scope:    core.ClientScope{BaseDir: req.Dir},
	}
	results, err := tool.BulkGetSymbolDefinitions(ctx, *req.SymbolDefinitions)
	response := ExecResponse{ID: req.ID}
	if err == nil {
		response.Stdout, err = json.Marshal(results)
	}
	if err != nil {
		response.Error = err.Error()
		response.ExitStatus = -1
	}
	return response
}