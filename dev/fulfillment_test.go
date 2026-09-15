package dev

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRenderFulfillmentInitial(t *testing.T) {
	t.Parallel()

	t.Run("with hints and auto checks", func(t *testing.T) {
		t.Parallel()
		data := map[string]interface{}{
			"editCodeHints": "Use tabs for indentation.\nWrite tests.",
			"requirements":  "Make it work.",
			"work":          "diff content here",
			"autoChecks":    "all passed",
		}
		out := RenderPrompt(FulfillmentInitial, data)

		assert.Contains(t, out, "Here are general repo-level guidelines for agents:")
		assert.Contains(t, out, "Use tabs for indentation.")
		assert.Contains(t, out, "# START REQUIREMENTS")
		assert.Contains(t, out, "Make it work.")
		assert.Contains(t, out, "diff content here")
		assert.Contains(t, out, "all passed")
		assert.Contains(t, out, "automated check results")

		// Hints should appear right before the requirements section, with no
		// leftover CODING HINTS labeling.
		assert.NotContains(t, out, "CODING HINTS")
		hintsIdx := strings.Index(out, "Here are general repo-level guidelines for agents:")
		reqIdx := strings.Index(out, "Here are all the requirements:")
		assert.True(t, hintsIdx > 0 && reqIdx > hintsIdx, "hints should appear before requirements heading")
	})

	t.Run("without hints or auto checks", func(t *testing.T) {
		t.Parallel()
		data := map[string]interface{}{
			"editCodeHints": "",
			"requirements":  "Make it work.",
			"work":          "diff content here",
			"autoChecks":    "",
		}
		out := RenderPrompt(FulfillmentInitial, data)

		assert.NotContains(t, out, "Here are general repo-level guidelines for agents:")
		assert.NotContains(t, out, "automated check results")
		assert.Contains(t, out, "Here are all the requirements:")
		assert.Contains(t, out, "Make it work.")
		assert.Contains(t, out, "diff content here")
	})
}

func TestRenderFulfillmentInitialWithPlan(t *testing.T) {
	t.Parallel()

	t.Run("with hints and auto checks", func(t *testing.T) {
		t.Parallel()
		data := map[string]interface{}{
			"editCodeHints":      "Use tabs for indentation.",
			"requirements":       "Make it work.",
			"work":               "diff content here",
			"autoChecks":         "all passed",
			"planContext":        "step 1: do thing",
			"currentStep":        "do the thing",
			"completionCriteria": "thing is done",
		}
		out := RenderPrompt(FulfillmentInitialWithPlan, data)

		assert.Contains(t, out, "Here are general repo-level guidelines for agents:")
		assert.Contains(t, out, "Use tabs for indentation.")
		assert.Contains(t, out, "# START REQUIREMENTS")
		assert.Contains(t, out, "# START PLAN")
		assert.Contains(t, out, "# START CURRENT STEP")
		assert.Contains(t, out, "# START Completion Criteria")
		assert.Contains(t, out, "step 1: do thing")
		assert.Contains(t, out, "do the thing")
		assert.Contains(t, out, "thing is done")
		assert.Contains(t, out, "diff content here")
		assert.Contains(t, out, "all passed")
		assert.NotContains(t, out, "CODING HINTS")

		hintsIdx := strings.Index(out, "Here are general repo-level guidelines for agents:")
		reqIdx := strings.Index(out, "Here is a reminder of the original requirements")
		assert.True(t, hintsIdx > 0 && reqIdx > hintsIdx, "hints should appear before requirements reminder")
	})

	t.Run("without hints or auto checks", func(t *testing.T) {
		t.Parallel()
		data := map[string]interface{}{
			"editCodeHints":      "",
			"requirements":       "Make it work.",
			"work":               "diff content here",
			"autoChecks":         "",
			"planContext":        "step 1: do thing",
			"currentStep":        "do the thing",
			"completionCriteria": "thing is done",
		}
		out := RenderPrompt(FulfillmentInitialWithPlan, data)

		assert.NotContains(t, out, "Here are general repo-level guidelines for agents:")
		assert.NotContains(t, out, "Anyways, here are the automated check results:")
		assert.NotContains(t, out, "And coming up are results of automated checks")
		assert.Contains(t, out, "step 1: do thing")
		assert.Contains(t, out, "thing is done")
	})
}

func TestRenderFulfillmentConflictResolution(t *testing.T) {
	t.Parallel()

	t.Run("frames requirements and review as context, not criteria", func(t *testing.T) {
		t.Parallel()
		data := map[string]interface{}{
			"editCodeHints":  "Use tabs for indentation.",
			"requirements":   "Implement feature X with steps a, b, c.",
			"previousReview": "Please rename the helper to doThing.",
			"work":           "conflict resolution diff here",
			"autoChecks":     "all passed",
		}
		out := RenderPrompt(FulfillmentConflictResolution, data)

		assert.Contains(t, out, "merge conflict resolution")
		assert.Contains(t, out, "ONLY criterion")
		assert.Contains(t, out, "# START CONTEXT: REQUIREMENTS")
		assert.Contains(t, out, "Implement feature X with steps a, b, c.")
		assert.Contains(t, out, "# START CONTEXT: ACCUMULATED REVIEW FEEDBACK")
		assert.Contains(t, out, "Please rename the helper to doThing.")
		assert.Contains(t, out, "conflict resolution diff here")
		assert.Contains(t, out, "all passed")

		// It must not frame the original requirements as the thing being fulfilled.
		assert.NotContains(t, out, "# START REQUIREMENTS")
	})

	t.Run("omits optional sections when empty", func(t *testing.T) {
		t.Parallel()
		data := map[string]interface{}{
			"editCodeHints":  "",
			"requirements":   "Implement feature X.",
			"previousReview": "",
			"work":           "conflict resolution diff here",
			"autoChecks":     "",
		}
		out := RenderPrompt(FulfillmentConflictResolution, data)

		assert.NotContains(t, out, "ACCUMULATED REVIEW FEEDBACK")
		assert.NotContains(t, out, "automated check results")
		assert.Contains(t, out, "conflict resolution diff here")
	})
}

func TestRequirementsWithoutPriorWork(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "generated context",
			input: formatRequirementsWithReview("requirements", []string{"older feedback"}, "old diff", "latest feedback"),
			want:  formatRequirementsWithReview("requirements", []string{"older feedback"}, "", "latest feedback"),
		},
		{
			name:  "heading inside original requirements",
			input: formatRequirementsWithReview("Work Done So Far:\n\nkeep this", nil, "old diff", "latest feedback"),
			want:  formatRequirementsWithReview("Work Done So Far:\n\nkeep this", nil, "", "latest feedback"),
		},
		{
			name:  "unwrapped requirements",
			input: "requirements\n\nWork Done So Far:\n\nuser text",
			want:  "requirements\n\nWork Done So Far:\n\nuser text",
		},
		{
			name:  "no generated work",
			input: formatRequirementsWithReview("requirements", nil, "", "Work Done So Far:\n\nfeedback text"),
			want:  formatRequirementsWithReview("requirements", nil, "", "Work Done So Far:\n\nfeedback text"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, requirementsWithoutPriorWork(tc.input))
		})
	}
}

func TestRequirementsWithoutPriorWorkDelimiterCollisions(t *testing.T) {
	t.Parallel()
	const workHeading = "\n\nWork Done So Far:\n\n"
	const feedbackIntro = "\n\nGiven the above context, please address the following latest user feedback:\n\n"
	for _, tc := range []struct {
		name     string
		original string
		history  []string
		work     string
		latest   string
	}{
		{
			name:     "work heading in historical feedback",
			original: "requirements", history: []string{"keep" + workHeading + "all this feedback"},
			work: "old diff", latest: "latest feedback",
		},
		{
			name:     "feedback introduction in historical feedback",
			original: "requirements", history: []string{"keep" + feedbackIntro + "all this feedback"},
			work: "old diff", latest: "latest feedback",
		},
		{
			name:     "feedback introduction in prior work",
			original: "requirements", history: []string{"historical feedback"},
			work: "old diff" + feedbackIntro + "more old diff", latest: "latest feedback",
		},
		{
			name:     "all delimiters in every component",
			original: "requirements#END Original Requirements\n\n" + workHeading + feedbackIntro,
			history:  []string{workHeading + feedbackIntro, feedbackIntro + workHeading},
			work:     workHeading + feedbackIntro + workHeading,
			latest:   feedbackIntro + workHeading + "retain latest feedback",
		},
		{
			name:     "historical work heading without generated work",
			original: "requirements", history: []string{workHeading + "retain this"},
			latest: "latest feedback",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := formatRequirementsWithReview(tc.original, tc.history, tc.work, tc.latest)
			want := formatRequirementsWithReview(tc.original, tc.history, "", tc.latest)
			assert.Equal(t, want, requirementsWithoutPriorWork(input))
		})
	}
}
