package dev

import (
	"sidekick/domain"
	"sidekick/env"
	"sidekick/flow_action"
	"slices"
)

const (
	globalStateKeyCurrentEnv  = "currentEnv"
	globalStateKeyEnvSwitch   = "envSwitch"
	globalStateKeyEnvLineage  = "envLineage"
	globalStateKeyRetiredEnvs = "retiredEnvs"
)

// envSwitchState describes an env switch in progress. Target and
// TargetWorktree are set once the target env exists, so it can still be
// cleaned up if the flow ends before the switch does.
type envSwitchState struct {
	FlowActionId   string
	From           string
	To             string
	Target         *env.EnvContainer
	TargetWorktree *domain.Worktree
}

// retiredEnv is an env the flow no longer runs on. It is kept until flow end
// rather than deleted when switching away, so resetting the workflow to a
// point where it was active finds it intact.
type retiredEnv struct {
	EnvContainer env.EnvContainer
	Worktree     *domain.Worktree
}

// flowEnvState tracks the env a flow currently runs on, every env it has run
// on, and any switch in progress. It lives in GlobalState so that replaying
// or resetting the workflow reconstructs it as of that point in history.
type flowEnvState struct {
	gs *flow_action.GlobalState
}

var _ env.EnvTracker = flowEnvState{}

func newFlowEnvState(gs *flow_action.GlobalState) flowEnvState {
	return flowEnvState{gs: gs}
}

// publish makes container the flow's current env. The pointer is retained, so
// later assignments through it are resolved without republishing; republishing
// is still required to recognize a new identity as part of the flow.
func (s flowEnvState) publish(container *env.EnvContainer) {
	s.gs.SetValue(globalStateKeyCurrentEnv, container)
	identity := env.IdentityOf(*container)
	lineage := s.lineage()
	if !slices.Contains(lineage, identity) {
		s.gs.SetValue(globalStateKeyEnvLineage, append(slices.Clone(lineage), identity))
	}
}

func (s flowEnvState) current() *env.EnvContainer {
	current, _ := s.gs.GetValue(globalStateKeyCurrentEnv).(*env.EnvContainer)
	return current
}

func (s flowEnvState) lineage() []env.EnvIdentity {
	lineage, _ := s.gs.GetValue(globalStateKeyEnvLineage).([]env.EnvIdentity)
	return lineage
}

func (s flowEnvState) SwitchInProgress() bool {
	return s.envSwitch() != nil
}

// Resolve redirects any env the flow has run on to the current one, leaving
// envs that never belonged to the flow untouched.
func (s flowEnvState) Resolve(c env.EnvContainer) env.EnvContainer {
	current := s.current()
	if current == nil || current.Env == nil || !slices.Contains(s.lineage(), env.IdentityOf(c)) {
		return c
	}
	return *current
}

func (s flowEnvState) setEnvSwitch(state envSwitchState) {
	s.gs.SetValue(globalStateKeyEnvSwitch, &state)
}

func (s flowEnvState) envSwitch() *envSwitchState {
	state, _ := s.gs.GetValue(globalStateKeyEnvSwitch).(*envSwitchState)
	return state
}

func (s flowEnvState) clearEnvSwitch() {
	s.gs.SetValue(globalStateKeyEnvSwitch, nil)
}

func (s flowEnvState) retiredEnvs() []retiredEnv {
	retired, _ := s.gs.GetValue(globalStateKeyRetiredEnvs).([]retiredEnv)
	return retired
}

// retire records an env for cleanup at flow end. Retiring an env again
// replaces its earlier entry, since switching back to an env reuses it.
func (s flowEnvState) retire(entry retiredEnv) {
	identity := env.IdentityOf(entry.EnvContainer)
	retired := slices.DeleteFunc(slices.Clone(s.retiredEnvs()), func(r retiredEnv) bool {
		return env.IdentityOf(r.EnvContainer) == identity
	})
	s.gs.SetValue(globalStateKeyRetiredEnvs, append(retired, entry))
}

// takeRetiredOfType removes and returns the most recently retired env of the
// given type, so a switch back to that type can reuse it.
func (s flowEnvState) takeRetiredOfType(envType env.EnvType) *retiredEnv {
	retired := s.retiredEnvs()
	for i := len(retired) - 1; i >= 0; i-- {
		if env.IdentityOf(retired[i].EnvContainer).EnvType != envType {
			continue
		}
		entry := retired[i]
		s.gs.SetValue(globalStateKeyRetiredEnvs, slices.Delete(slices.Clone(retired), i, i+1))
		return &entry
	}
	return nil
}

// unretire drops the retired entry of an env the flow runs on again, since
// it is now cleaned up as the active env.
func (s flowEnvState) unretire(identity env.EnvIdentity) {
	retired := s.retiredEnvs()
	isIdentity := func(r retiredEnv) bool { return env.IdentityOf(r.EnvContainer) == identity }
	if !slices.ContainsFunc(retired, isIdentity) {
		return
	}
	s.gs.SetValue(globalStateKeyRetiredEnvs, slices.DeleteFunc(slices.Clone(retired), isIdentity))
}

// recordSwitchTarget keeps the most complete known state of the env being
// switched to, so whatever was provisioned can be retired if the switch
// does not complete.
func (s flowEnvState) recordSwitchTarget(target ensuredEnv) {
	current := s.envSwitch()
	if current == nil {
		return
	}
	updated := *current
	container := target.EnvContainer
	updated.Target = &container
	updated.TargetWorktree = target.Worktree
	s.setEnvSwitch(updated)
}
