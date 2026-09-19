package env

import "errors"

// ErrGitTagConflict indicates that syncing a tag would overwrite a different
// local tag. Callers may retry with a new archive name without losing either ref.
var ErrGitTagConflict = errors.New("git tag conflicts with existing local tag")
