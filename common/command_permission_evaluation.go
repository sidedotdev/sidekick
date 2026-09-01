package common

import (
	"sidekick/coding/permission"
)

// Constants identifying which merged config layer a CommandPattern came from.
// Set programmatically at merge time so evaluation metadata can attribute each
// rule to its origin.
const (
	CommandPatternSourceBase                = "base"
	CommandPatternSourceBaseIsolatedSandbox = "base_isolated_sandbox"
	CommandPatternSourceLocalConfig         = "local_config"
	CommandPatternSourceRepoConfig          = "repo_config"
	CommandPatternSourceWorkspaceConfig     = "workspace_config"
)

// TagCommandPatternSources returns a copy of config with Source set on every
// pattern in all three lists, attributing them to the given config layer (see
// CommandPatternSource* constants). The input config is not mutated.
func TagCommandPatternSources(config CommandPermissionConfig, source string) CommandPermissionConfig {
	tag := func(patterns []CommandPattern) []CommandPattern {
		if len(patterns) == 0 {
			return patterns
		}
		tagged := make([]CommandPattern, len(patterns))
		for i, p := range patterns {
			p.Source = source
			tagged[i] = p
		}
		return tagged
	}
	config.AutoApprove = tag(config.AutoApprove)
	config.RequireApproval = tag(config.RequireApproval)
	config.Deny = tag(config.Deny)
	return config
}

// PermissionFactorKind identifies a non-pattern cause that influenced a
// permission evaluation outcome. New kinds may be added over time.
type PermissionFactorKind string

const (
	PermissionFactorNoRuleMatched             PermissionFactorKind = "no_rule_matched"
	PermissionFactorAbsolutePathEscalation    PermissionFactorKind = "absolute_path_escalation"
	PermissionFactorSandboxDefaultAutoApprove PermissionFactorKind = "sandbox_default_auto_approve"
	PermissionFactorHeredocFileWriteDeny      PermissionFactorKind = "heredoc_file_write_deny"
	PermissionFactorSandboxHeredocAdvisory    PermissionFactorKind = "sandbox_heredoc_advisory"
	PermissionFactorHeredocEscapeHatch        PermissionFactorKind = "heredoc_escape_hatch"
	PermissionFactorTempPathAdvisory          PermissionFactorKind = "temp_path_advisory"
	PermissionFactorEmptyCommandExtraction    PermissionFactorKind = "empty_command_extraction"
)

// PermissionDecider discriminates whether a matched rule or a synthesized
// factor determined an evaluation outcome.
type PermissionDecider string

const (
	DecidedByRule   PermissionDecider = "rule"
	DecidedByFactor PermissionDecider = "factor"
)

// MatchedRule records a single config pattern that matched a command during
// evaluation, regardless of whether it determined the outcome.
type MatchedRule struct {
	// Action is the permission list the rule belongs to.
	Action  PermissionResult `json:"action"`
	Pattern string           `json:"pattern"`
	// Message is the rule's message with any capture groups interpolated.
	Message string `json:"message,omitempty"`
	// Source attributes the rule to a merged config layer (see
	// CommandPatternSource* constants).
	Source string `json:"source,omitempty"`
}

// PermissionFactor represents a non-pattern influence on an evaluation
// outcome, e.g. absolute-path escalation or the no-rule-matched default.
type PermissionFactor struct {
	Kind PermissionFactorKind `json:"kind"`
	// Outcome is the result this factor implies or contributes, if any.
	// Advisory factors leave it empty.
	Outcome PermissionResult `json:"outcome,omitempty"`
	Message string           `json:"message,omitempty"`
	// Paths lists any paths that triggered this factor, e.g. detected
	// absolute paths for absolute_path_escalation.
	Paths []string `json:"paths,omitempty"`
}

// CommandEvaluation captures the full permission evaluation of a single
// extracted command: every rule that matched (winning or overridden),
// synthesized factors, and which of them decided the outcome.
type CommandEvaluation struct {
	Command      string             `json:"command"`
	Outcome      PermissionResult   `json:"outcome"`
	MatchedRules []MatchedRule      `json:"matchedRules,omitempty"`
	Factors      []PermissionFactor `json:"factors,omitempty"`
	// DecidedBy tells whether MatchedRules[DecidedByIndex] or
	// Factors[DecidedByIndex] determined Outcome.
	DecidedBy      PermissionDecider `json:"decidedBy"`
	DecidedByIndex int               `json:"decidedByIndex"`
}

// ScriptPermissionEvaluation captures the full permission evaluation of a
// script: per-extracted-command evaluations plus script-level factors (e.g.
// heredoc file-write handling, temp-path advisories).
type ScriptPermissionEvaluation struct {
	Outcome  PermissionResult    `json:"outcome"`
	Commands []CommandEvaluation `json:"commands,omitempty"`
	Factors  []PermissionFactor  `json:"factors,omitempty"`
}

// EvaluateCommandPermissionDetailed evaluates a single command against all
// three permission lists without short-circuiting, recording every matched
// rule and synthesizing factors for non-pattern causes. Precedence is
// unchanged: deny > require_approval > auto_approve, with auto-approve
// matches escalating to require_approval when the command references absolute
// paths (unless opts.SkipAbsolutePathEscalation).
func EvaluateCommandPermissionDetailed(config CommandPermissionConfig, command string, opts EvaluatePermissionOptions) CommandEvaluation {
	strippedCommand := command
	if opts.StripEnvVarPrefix {
		strippedCommand = stripEnvVarPrefix(command)
	}

	eval := CommandEvaluation{Command: command}

	// collectMatches appends every matching pattern from one list and returns
	// the index (into eval.MatchedRules) of the list's first match, or -1.
	collectMatches := func(patterns []CommandPattern, action PermissionResult) int {
		firstIdx := -1
		for _, p := range patterns {
			// Use original command if pattern contains env vars, otherwise use stripped
			cmdToMatch := command
			if !patternContainsEnvVar(p.Pattern) {
				cmdToMatch = strippedCommand
			}
			if matched, matches := matchPattern(p.Pattern, cmdToMatch); matched {
				msg := p.Message
				if msg != "" && len(matches) > 0 {
					msg = interpolateMessage(msg, matches)
				}
				eval.MatchedRules = append(eval.MatchedRules, MatchedRule{
					Action:  action,
					Pattern: p.Pattern,
					Message: msg,
					Source:  p.Source,
				})
				if firstIdx == -1 {
					firstIdx = len(eval.MatchedRules) - 1
				}
			}
		}
		return firstIdx
	}

	denyIdx := collectMatches(config.Deny, PermissionDeny)
	requireIdx := collectMatches(config.RequireApproval, PermissionRequireApproval)
	autoIdx := collectMatches(config.AutoApprove, PermissionAutoApprove)

	decideByFactor := func(factor PermissionFactor) {
		eval.Factors = append(eval.Factors, factor)
		eval.Outcome = factor.Outcome
		eval.DecidedBy = DecidedByFactor
		eval.DecidedByIndex = len(eval.Factors) - 1
	}

	switch {
	case denyIdx >= 0:
		eval.Outcome = PermissionDeny
		eval.DecidedBy = DecidedByRule
		eval.DecidedByIndex = denyIdx
	case requireIdx >= 0:
		eval.Outcome = PermissionRequireApproval
		eval.DecidedBy = DecidedByRule
		eval.DecidedByIndex = requireIdx
	case autoIdx >= 0:
		// Even if auto-approved, require approval for commands with absolute paths
		if !opts.SkipAbsolutePathEscalation && containsAbsolutePath(command) {
			decideByFactor(PermissionFactor{
				Kind:    PermissionFactorAbsolutePathEscalation,
				Outcome: PermissionRequireApproval,
				Message: "auto-approve escalated to require approval because the command references absolute path(s)",
				Paths:   detectedAbsolutePaths(command),
			})
		} else {
			eval.Outcome = PermissionAutoApprove
			eval.DecidedBy = DecidedByRule
			eval.DecidedByIndex = autoIdx
		}
	case opts.DefaultAutoApprove:
		decideByFactor(PermissionFactor{
			Kind:    PermissionFactorSandboxDefaultAutoApprove,
			Outcome: PermissionAutoApprove,
			Message: "no permission rule matched; unmatched commands auto-approve in this sandbox",
		})
	default:
		decideByFactor(PermissionFactor{
			Kind:    PermissionFactorNoRuleMatched,
			Outcome: PermissionRequireApproval,
			Message: "no permission rule matched; unmatched commands require approval by default",
		})
	}

	return eval
}

// EvaluateScriptPermissionDetailed evaluates a shell script by extracting all
// commands and evaluating each one in detail, recording script-level factors
// (heredoc file-write handling, temp-path advisories, empty extraction) and
// per-command evaluations. The overall outcome aggregates as: any deny →
// deny; else any require_approval → require_approval; else auto_approve.
// Unlike the (result, message) wrapper it does not short-circuit on the first
// denying command, so all command evaluations are always recorded.
func EvaluateScriptPermissionDetailed(config CommandPermissionConfig, script string, opts EvaluatePermissionOptions) ScriptPermissionEvaluation {
	scriptEval := ScriptPermissionEvaluation{}
	heredocWrites := permission.DetectHeredocFileWrites(script)
	if tempPathPattern.MatchString(script) {
		scriptEval.Factors = append(scriptEval.Factors, PermissionFactor{
			Kind:    PermissionFactorTempPathAdvisory,
			Message: tempPathAdvisory,
		})
	}
	if opts.HeredocFileWriteWarnInsteadOfDeny {
		if len(heredocWrites) > 0 {
			scriptEval.Factors = append(scriptEval.Factors, PermissionFactor{
				Kind:    PermissionFactorSandboxHeredocAdvisory,
				Message: SandboxHeredocFileWriteAdvisory,
			})
		}
	} else {
		for _, hw := range heredocWrites {
			if !hw.UsesEscapeHatch() {
				scriptEval.Factors = append(scriptEval.Factors, PermissionFactor{
					Kind:    PermissionFactorHeredocFileWriteDeny,
					Outcome: PermissionDeny,
					Message: heredocFileWriteDenyMessage,
				})
				scriptEval.Outcome = PermissionDeny
				return scriptEval
			}
		}
	}

	var commands []string
	if opts.UseLegacyCommandExtraction {
		commands = permission.ExtractCommandsLegacy(script)
	} else {
		commands = permission.ExtractCommands(script)
	}

	if len(commands) == 0 {
		factor := PermissionFactor{Kind: PermissionFactorEmptyCommandExtraction}
		if opts.DefaultAutoApprove {
			factor.Outcome = PermissionAutoApprove
			factor.Message = "no commands were extracted from the script; empty extraction auto-approves in this sandbox"
		} else {
			factor.Outcome = PermissionRequireApproval
			factor.Message = "no commands were extracted from the script; empty extraction requires approval by default"
		}
		scriptEval.Factors = append(scriptEval.Factors, factor)
		scriptEval.Outcome = factor.Outcome
		return scriptEval
	}

	scriptEval.Outcome = PermissionAutoApprove
	for _, cmd := range commands {
		cmdEval := EvaluateCommandPermissionDetailed(config, cmd, opts)
		// Commands using the documented heredoc escape-hatch delimiter
		// bypass the heredoc deny patterns and are forced through approval
		// so a human can vet them. In sandbox mode the escape hatch is
		// irrelevant (heredoc writes are allowed outright) so we skip this.
		// Matched rules and other factors are retained in the metadata; the
		// escape-hatch factor overrides them as the decision.
		if !opts.HeredocFileWriteWarnInsteadOfDeny && commandUsesHeredocEscapeHatch(cmd) {
			cmdEval.Factors = append(cmdEval.Factors, PermissionFactor{
				Kind:    PermissionFactorHeredocEscapeHatch,
				Outcome: PermissionRequireApproval,
				Message: "heredoc escape-hatch delimiter used; the file write is allowed but requires explicit approval",
			})
			cmdEval.Outcome = PermissionRequireApproval
			cmdEval.DecidedBy = DecidedByFactor
			cmdEval.DecidedByIndex = len(cmdEval.Factors) - 1
		}
		scriptEval.Commands = append(scriptEval.Commands, cmdEval)

		switch cmdEval.Outcome {
		case PermissionDeny:
			scriptEval.Outcome = PermissionDeny
		case PermissionRequireApproval:
			if scriptEval.Outcome != PermissionDeny {
				scriptEval.Outcome = PermissionRequireApproval
			}
		}
	}

	return scriptEval
}

// commandDecisionMessage returns the message of the rule that decided a
// command's outcome, or "" when a factor decided it (factor messages are
// synthesized explanations, not user-facing evaluation messages).
func commandDecisionMessage(c CommandEvaluation) string {
	if c.DecidedBy == DecidedByRule && c.DecidedByIndex < len(c.MatchedRules) {
		return c.MatchedRules[c.DecidedByIndex].Message
	}
	return ""
}

// detectedAbsolutePaths collects the absolute paths that trigger
// absolute-path escalation, mirroring containsAbsolutePath's filtering of
// regex arguments and safe paths.
func detectedAbsolutePaths(command string) []string {
	parts := parseCommandForPaths(command)
	regexArgIndices := detectRegexArguments(parts)

	var paths []string
	for i, part := range parts {
		if regexArgIndices[i] {
			continue
		}
		paths = append(paths, unsafeAbsolutePathsInPart(part)...)
	}
	return paths
}
