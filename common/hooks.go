package common

import (
	"fmt"
	"math"
	"time"
)

const (
	HookEventRunTests             = "run_tests"
	HookEventRunCommand           = "run_command"
	HookActionModalRefreshVolumes = "modal.refresh_volumes"
	HookActionScript              = "script"
	HookOnErrorFail               = "fail"
	HookOnErrorWarn               = "warn"
)

type HooksConfig struct {
	RunTests   []HookRuleConfig `koanf:"run_tests,omitempty" json:"runTests,omitempty"`
	RunCommand []HookRuleConfig `koanf:"run_command,omitempty" json:"runCommand,omitempty"`
}

type HookRuleConfig struct {
	Match   *HookMatchConfig   `koanf:"match,omitempty" json:"match,omitempty"`
	Actions []HookActionConfig `koanf:"actions" json:"actions"`
}

type HookMatchConfig struct {
	EnvTypes []string `koanf:"env_types,omitempty" json:"envTypes,omitempty"`
}

type HookActionConfig struct {
	Type           string   `koanf:"type" json:"type"`
	Source         string   `koanf:"source,omitempty" json:"source,omitempty"`
	TimeoutSeconds *float64 `koanf:"timeout_seconds,omitempty" json:"timeoutSeconds,omitempty"`
	OnError        *string  `koanf:"on_error,omitempty" json:"onError,omitempty"`
}

func (c HooksConfig) Validate() error {
	if err := validateHookRules(HookEventRunTests, c.RunTests); err != nil {
		return err
	}
	return validateHookRules(HookEventRunCommand, c.RunCommand)
}

func validateHookRules(event string, rules []HookRuleConfig) error {
	for i, rule := range rules {
		location := fmt.Sprintf("hooks.%s[%d]", event, i)
		if err := rule.Validate(location); err != nil {
			return err
		}
		for j, action := range rule.Actions {
			if err := action.Validate(fmt.Sprintf("%s.actions[%d]", location, j)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r HookRuleConfig) Validate(location string) error {
	if r.Actions == nil {
		return fmt.Errorf("%s.actions: an action list is required", location)
	}
	if r.Match == nil || r.Match.EnvTypes == nil {
		return nil
	}
	if len(r.Match.EnvTypes) == 0 {
		return fmt.Errorf("%s.match.env_types: must not be empty", location)
	}
	for i, name := range r.Match.EnvTypes {
		switch name {
		case "local", "local_git_worktree", "devpod", "openshell", "modal":
		default:
			return fmt.Errorf("%s.match.env_types[%d]: unsupported environment %q", location, i, name)
		}
	}
	return nil
}

func (a HookActionConfig) Validate(location string) error {
	location = fmt.Sprintf("%s (%s)", location, a.Type)
	switch a.Type {
	case HookActionScript:
		if a.Source == "" {
			return fmt.Errorf("%s.source: a script source is required", location)
		}
	case HookActionModalRefreshVolumes:
		if a.Source != "" {
			return fmt.Errorf("%s.source: only supported for %s actions", location, HookActionScript)
		}
	default:
		return fmt.Errorf("%s: unsupported action type %q", location, a.Type)
	}
	if a.TimeoutSeconds != nil {
		ns := *a.TimeoutSeconds * float64(time.Second)
		// The upper bound is exclusive because float64 rounds MaxInt64 up.
		if math.IsNaN(ns) || math.IsInf(ns, 0) || ns < 1 || ns >= float64(math.MaxInt64) {
			return fmt.Errorf("%s.timeout_seconds: must be positive and representable as a duration", location)
		}
	}
	if policy := a.ErrorPolicy(); policy != HookOnErrorFail && policy != HookOnErrorWarn {
		return fmt.Errorf("%s.on_error: must be fail or warn", location)
	}
	return nil
}

// Timeout returns the configured duration after validation.
func (a HookActionConfig) Timeout() time.Duration {
	if a.TimeoutSeconds == nil {
		return 30 * time.Second
	}
	return time.Duration(*a.TimeoutSeconds * float64(time.Second))
}

func (a HookActionConfig) ErrorPolicy() string {
	if a.OnError == nil {
		return HookOnErrorFail
	}
	return *a.OnError
}
