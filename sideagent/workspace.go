package sideagent

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"sidekick/repotools"
)

// WorkspaceRequest keeps dependent repository operations on the same side of
// the connection. Exactly one operation must be supplied.
type WorkspaceRequest struct {
	ReadFiles     []string              `json:"readFiles,omitempty"`
	Search        *WorkspaceSearch      `json:"search,omitempty"`
	PrepareSearch *WorkspaceSearchSetup `json:"prepareSearch,omitempty"`
}

type WorkspaceResponse struct {
	// Omitted files must be read individually. This includes missing files,
	// non-regular files and files exceeding the prefetch budget.
	Files          map[string][]byte      `json:"files,omitempty"`
	Search         *WorkspaceSearchResult `json:"search,omitempty"`
	PreparedSearch *WorkspaceSearchState  `json:"preparedSearch,omitempty"`
	// Fallback asks the caller to use its individual operations when the
	// combined response would exceed the workspace response budget.
	Fallback bool `json:"fallback,omitempty"`
}

// WorkspaceSearch uses the caller's search commands so ignore and regex
// policies remain identical to the non-RPC path.
type WorkspaceSearch struct {
	ListCommand   string `json:"listCommand"`
	SearchCommand string `json:"searchCommand"`
	PathGlob      string `json:"pathGlob"`
	ListSeparator string `json:"listSeparator,omitempty"`
}

type WorkspaceSearchResult struct {
	Listing      ExecResponse `json:"listing"`
	Output       ExecResponse `json:"output"`
	AllFiles     []string     `json:"allFiles"`
	MatchedFiles []string     `json:"matchedFiles"`
}

const workspaceReadBudget = 8 << 20

func (s *server) executeWorkspace(req ExecRequest) ExecResponse {
	resp := ExecResponse{ID: req.ID}
	op := req.Workspace
	result := WorkspaceResponse{}
	var err error
	switch {
	case len(op.ReadFiles) > 0 && op.Search == nil && op.PrepareSearch == nil:
		result.Files, err = s.readWorkspaceFiles(req)
	case len(op.ReadFiles) == 0 && op.Search != nil && op.PrepareSearch == nil:
		result.Search, err = s.searchWorkspace(req)
	case len(op.ReadFiles) == 0 && op.Search == nil && op.PrepareSearch != nil:
		result.PreparedSearch, err = prepareWorkspaceSearch(req.Dir, *op.PrepareSearch)
	default:
		err = fmt.Errorf("expected exactly one workspace operation")
	}
	if err == nil {
		resp.Stdout, err = json.Marshal(result)
	}
	// Stdout is base64-encoded again by the outer response envelope.
	if err == nil && len(resp.Stdout) > 16<<20 {
		resp.Stdout, err = json.Marshal(WorkspaceResponse{Fallback: true})
	}
	if err != nil {
		resp.Stdout = nil
		resp.Error = err.Error()
		resp.ExitStatus = -1
	}
	return resp
}

func (s *server) workspaceCanceled(id uint64) bool {
	if s.isCanceled(id) {
		return true
	}
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *server) readWorkspaceFiles(req ExecRequest) (map[string][]byte, error) {
	if len(req.Workspace.ReadFiles) > 4096 {
		return nil, fmt.Errorf("too many files to prefetch")
	}
	files := make(map[string][]byte)
	remaining := int64(workspaceReadBudget)
	for _, name := range req.Workspace.ReadFiles {
		if s.workspaceCanceled(req.ID) {
			return nil, fmt.Errorf("workspace read canceled")
		}
		if _, ok := files[name]; ok {
			continue
		}
		path := name
		if !filepath.IsAbs(path) {
			path = filepath.Join(req.Dir, path)
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > remaining {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(f, remaining+1))
		f.Close()
		if err != nil || int64(len(data)) > remaining {
			continue
		}
		files[name] = data
		remaining -= int64(len(data))
	}
	return files, nil
}

func (s *server) searchWorkspace(req ExecRequest) (*WorkspaceSearchResult, error) {
	search := req.Workspace.Search
	command := req
	command.Workspace = nil
	command.Argv = []string{"sh", "-c", search.ListCommand}
	result := &WorkspaceSearchResult{Listing: s.executeCommand(command)}
	if result.Listing.Error != "" {
		return nil, fmt.Errorf("list search files: %s", result.Listing.Error)
	}
	if s.workspaceCanceled(req.ID) {
		return nil, fmt.Errorf("workspace search canceled")
	}
	listing := string(result.Listing.Stdout)
	if search.ListSeparator != "" {
		if _, after, found := strings.Cut(listing, search.ListSeparator+"\n"); found {
			listing = after
		}
	}
	if listing = strings.TrimSpace(listing); listing != "" {
		result.AllFiles = strings.Split(listing, "\n")
	}
	var err error
	result.MatchedFiles, err = repotools.FilterFilesByGlob(result.AllFiles, search.PathGlob)
	if err != nil {
		return nil, err
	}
	if len(result.MatchedFiles) == 0 {
		return result, nil
	}
	var args strings.Builder
	args.WriteString(search.SearchCommand)
	for _, file := range result.MatchedFiles {
		args.WriteString(" '")
		args.WriteString(strings.ReplaceAll(file, "'", "'\\''"))
		args.WriteByte('\'')
	}
	command.Argv = []string{"sh", "-c", args.String()}
	result.Output = s.executeCommand(command)
	if result.Output.Error != "" {
		return nil, fmt.Errorf("search filtered files: %s", result.Output.Error)
	}
	return result, nil
}

type WorkspaceSearchSetup struct {
	IgnorePath    string `json:"ignorePath"`
	IgnoreContent string `json:"ignoreContent"`
}

type WorkspaceSearchState struct {
	SideIgnoreExists bool `json:"sideIgnoreExists"`
	GitIgnoreExists  bool `json:"gitIgnoreExists"`
}

func prepareWorkspaceSearch(dir string, setup WorkspaceSearchSetup) (*WorkspaceSearchState, error) {
	path := setup.IgnorePath
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	content, err := os.ReadFile(path)
	if err != nil || string(content) != setup.IgnoreContent {
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return nil, err
		}
		f, err := os.CreateTemp(filepath.Dir(path), "core-ignore-*")
		if err != nil {
			return nil, err
		}
		defer os.Remove(f.Name())
		if _, err = f.WriteString(setup.IgnoreContent); err == nil {
			err = f.Chmod(0644)
		}
		closeErr := f.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if err := os.Rename(f.Name(), path); err != nil {
			return nil, err
		}
	}
	readable := func(name string) bool {
		_, err := os.ReadFile(filepath.Join(dir, name))
		return err == nil
	}
	return &WorkspaceSearchState{
		SideIgnoreExists: readable(".sideignore"),
		GitIgnoreExists:  readable(".gitignore"),
	}, nil
}
