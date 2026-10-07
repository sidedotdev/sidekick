---
intent_links:
  - intent: "#assignment-recording"
    code:
      - fflag/evaluate_flags.go:EvaluateFlags
      - fflag/assignment_journal.go:appendFlagAssignment
  - intent: "#verifier-report-experiment"
    code:
      - flags.yml
      - dev/verifier_settings.go:verifierFlagsInput
      - dev/verifier_settings.go:verifierSettingsFromFlags
      - dev/verifier_settings_types.go:DefaultVerifierSettings
      - dev/verifier_chat_history.go:prepareVerifierMessages
---

# Flag assignment journal

## Problem

Experiment analysis requires a durable record of which flag variant each
evaluation selected and the targeting context used. Flag configuration alone
does not identify the assignments that produced observed outcomes.

## Assignment recording

- Record successful percentage-based assignments for boolean, integer, and
  string-array flags in private, persistent local state. Exclude fixed choices,
  missing flags, failed evaluations, and legacy boolean evaluation.
- Each record identifies the evaluation time, targeting key and attributes,
  flag, selected variant, resolved value, and flag version when available.
- Verifier assignments identify the workflow, flow type, and judging model.
  Records contain no chat content.
- Recording failures are observable and do not fail flag evaluation.
- Records represent evaluations, not unique users or review invocations;
  retries may produce duplicate assignments.

## Constraints

- Flag versions distinguish experiment rollouts.
- Automatic upload, rotation, and retention management are out of scope.

## Verifier report experiment

- `review_context_types` selects the generated report categories supplied to the
  verifier. Two variants receive equal traffic:
  - `withoutAutoReview`: `EditBlockReport`, `TestResult`, `Summary`.
  - `withAutoReview`: `EditBlockReport`, `TestResult`, `AutoReviewFeedback`,
    `Summary`.
- Evaluation failure excludes automatic-review feedback. An explicitly empty
  list excludes all generated report categories.
- Human guidance and tool records are selected independently of this flag.
- Verifier settings remain stable for the flow and are recorded in workflow
  history.
- Production uses the deployed remote flag configuration.