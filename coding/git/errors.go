package git

// MergeRejectedError indicates that a merge was rejected with feedback
type MergeRejectedError struct {
	Message string
}

func (e *MergeRejectedError) Error() string {
	return "merge rejected: " + e.Message
}
