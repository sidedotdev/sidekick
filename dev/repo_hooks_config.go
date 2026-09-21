package dev

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sidekick/common"
	"sort"

	"github.com/knadh/koanf/v2"
)

// decodeRepoConfig keeps legacy configuration permissive while validating hooks
// before weak decoding can erase the distinction between omitted and invalid values.
func decodeRepoConfig(k *koanf.Koanf, config *common.RepoConfig) error {
	var hooks common.HooksConfig
	if raw, exists := k.Raw()["hooks"]; exists {
		data, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("hooks: %w", err)
		}
		hooks, err = decodeRepoHooks(data)
		if err != nil {
			return err
		}
	}
	if err := k.Unmarshal("", config); err != nil {
		return err
	}
	config.Hooks = hooks
	return nil
}

func decodeRepoHooks(data []byte) (common.HooksConfig, error) {
	var config common.HooksConfig
	fields, err := hookObject(data, "hooks", "run_tests")
	if err != nil {
		return config, err
	}
	if raw, exists := fields["run_tests"]; exists {
		var rules []json.RawMessage
		if err := hookValue(raw, &rules, "hooks.run_tests"); err != nil {
			return config, err
		}
		config.RunTests = make([]common.HookRuleConfig, len(rules))
		for i, rawRule := range rules {
			location := fmt.Sprintf("hooks.run_tests[%d]", i)
			rule, err := decodeHookRule(rawRule, location)
			if err != nil {
				return config, err
			}
			config.RunTests[i] = rule
		}
	}
	return config, config.Validate()
}

func decodeHookRule(data []byte, location string) (common.HookRuleConfig, error) {
	var rule common.HookRuleConfig
	fields, err := hookObject(data, location, "match", "actions")
	if err != nil {
		return rule, err
	}
	if raw, exists := fields["match"]; exists {
		match, err := hookObject(raw, location+".match", "env_types")
		if err != nil {
			return rule, err
		}
		rule.Match = &common.HookMatchConfig{}
		if raw, exists := match["env_types"]; exists {
			if err := hookValue(raw, &rule.Match.EnvTypes, location+".match.env_types"); err != nil {
				return rule, err
			}
		}
	}
	var actions []json.RawMessage
	if err := hookValue(fields["actions"], &actions, location+".actions"); err != nil {
		return rule, err
	}
	rule.Actions = make([]common.HookActionConfig, len(actions))
	for i, raw := range actions {
		action, err := decodeHookAction(raw, fmt.Sprintf("%s.actions[%d]", location, i))
		if err != nil {
			return rule, err
		}
		rule.Actions[i] = action
	}
	return rule, nil
}

func decodeHookAction(data []byte, location string) (common.HookActionConfig, error) {
	var action common.HookActionConfig
	var fields map[string]json.RawMessage
	if err := hookValue(data, &fields, location); err != nil {
		return action, err
	}
	if err := hookValue(fields["type"], &action.Type, location+".type"); err != nil {
		return action, err
	}
	actionLocation := fmt.Sprintf("%s (%s)", location, action.Type)
	if _, err := hookObject(data, actionLocation, "type", "timeout_seconds", "on_error"); err != nil {
		return action, err
	}
	if raw, exists := fields["timeout_seconds"]; exists {
		action.TimeoutSeconds = new(float64)
		if err := hookValue(raw, action.TimeoutSeconds, actionLocation+".timeout_seconds"); err != nil {
			return action, err
		}
	}
	if raw, exists := fields["on_error"]; exists {
		action.OnError = new(string)
		if err := hookValue(raw, action.OnError, actionLocation+".on_error"); err != nil {
			return action, err
		}
	}
	return action, action.Validate(location)
}

func hookObject(data []byte, location string, allowed ...string) (map[string]json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := hookValue(data, &fields, location); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		found := false
		for _, name := range allowed {
			if key == name {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("%s.%s: unknown field", location, key)
		}
	}
	return fields, nil
}

func hookValue(data []byte, target interface{}, location string) error {
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("%s: must not be missing or null", location)
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("%s: %w", location, err)
	}
	return nil
}
