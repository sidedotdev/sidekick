package dev

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFeedbackContextType(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		feedback string
		marker   string
	}{
		{FeedbackTypeTestFailure, ContextTypeTestResult},
		{FeedbackTypeAutoReview, ContextTypeAutoReviewFeedback},
		{FeedbackTypeApplyError, ContextTypeEditBlockReport},
		{FeedbackTypeSystemError, ""},
		{"ordinary_tool_result", ""},
	} {
		t.Run(tc.feedback, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.marker, feedbackContextType(tc.feedback, false))
			assert.Equal(t, tc.marker, feedbackContextType(tc.feedback, true))
		})
	}
}
