package callers

import "context"

// ActivityFunc has only context.Context (not a workflow context) and does not call Append.
func ActivityFunc(ctx context.Context) error {
	return nil
}