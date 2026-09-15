package dev

import (
	"strings"

	"github.com/cbroglie/mustache"
)

func criteriaFulfillmentParts(info CheckWorkInfo, editCodeHints ...string) (string, string) {
	hints := ""
	if len(editCodeHints) > 0 {
		hints = editCodeHints[0]
	}
	data := map[string]interface{}{
		"editCodeHints":      hints,
		"requirements":       info.Requirements,
		"previousReview":     info.PreviousReview,
		"work":               info.Work,
		"autoChecks":         info.AutoChecks,
		"incrementalReview":  info.IncrementalReview,
		"planContext":        "",
		"currentStep":        info.Step.Definition,
		"completionCriteria": info.Step.CompletionAnalysis,
	}
	templateName := "fulfillment/initial"
	if info.ResolvingMergeConflicts {
		templateName = "fulfillment/conflict_resolution"
	} else if info.Step.Definition != "" {
		templateName = "fulfillment/initial_with_plan"
		data["planContext"] = info.PlanExecution.String()
	}
	source, err := promptsFS.ReadFile("prompts/" + templateName + ".mustache")
	if err != nil {
		panic(err)
	}
	prefix, suffix, found := strings.Cut(string(source), "{{> git_diff}}")
	if !found {
		panic("fulfillment template missing git_diff partial")
	}
	provider := &fsPartialProvider{fs: promptsFS, prefix: "fulfillment/"}
	render := func(source string) string {
		template, err := mustache.ParseStringPartials(source, provider)
		if err != nil {
			panic(err)
		}
		return RenderPrompt(template, data)
	}
	return render(prefix), render("{{> git_diff}}" + suffix)
}

func criteriaFulfillmentContent(info CheckWorkInfo, editCodeHints ...string) string {
	prefix, suffix := criteriaFulfillmentParts(info, editCodeHints...)
	return prefix + suffix
}
