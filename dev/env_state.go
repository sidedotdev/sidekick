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
	// StopRequested marks a sandbox the flow is stopping in the background
	// after superseding it. A switch back must not reattach to such a
	// sandbox: for Modal the stop is terminal.
	StopRequested bool
	// Stopped marks a sandbox already stopped when it was superseded, so
	// flow-end cleanup skips the steps that would need it running.
	Stopped bool
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
	retired := slices.Clone(s.retiredEnvs())
	for i := range retired {
		if !sameEnvInstance(retired[i].EnvContainer, entry.EnvContainer) {
			continue
		}
		previous := retired[i]
		// A sandbox retired mid-provisioning has no worktree yet; it must
		// not displace the record that knows which worktree to clean up.
		if entry.Worktree == nil && previous.Worktree != nil {
			entry.EnvContainer = previous.EnvContainer
			entry.Worktree = previous.Worktree
		}
		// Stop state describes the physical sandbox, so it survives the
		// record being replaced; only running on the env again (unretire)
		// resets it.
		entry.StopRequested = entry.StopRequested || previous.StopRequested
		entry.Stopped = entry.Stopped || previous.Stopped
		retired = slices.Delete(retired, i, i+1)
		break
	}
	s.gs.SetValue(globalStateKeyRetiredEnvs, append(retired, entry))
}

// retiredOfType returns the most recently retired env of the given type, so a
// switch back to that type can reattach to it. The entry stays retired until
// the switch publishes the env.
func (s flowEnvState) retiredOfType(envType env.EnvType) *retiredEnv {
	retired := s.retiredEnvs()
	for i := len(retired) - 1; i >= 0; i-- {
		if retired[i].EnvContainer.Env.GetType() == envType {
			entry := retired[i]
			return &entry
		}
	}
	return nil
}

// retiredEntry returns the retired record of container's env, if any.
func (s flowEnvState) retiredEntry(container env.EnvContainer) *retiredEnv {
	for _, r := range s.retiredEnvs() {
		if sameEnvInstance(r.EnvContainer, container) {
			entry := r
			return &entry
		}
	}
	return nil
}

func (s flowEnvState) markStopRequested(container env.EnvContainer) {
	s.updateRetired(container, func(r *retiredEnv) { r.StopRequested = true })
}

func (s flowEnvState) markStopped(container env.EnvContainer) {
	s.updateRetired(container, func(r *retiredEnv) { r.Stopped = true })
}

func (s flowEnvState) updateRetired(container env.EnvContainer, update func(*retiredEnv)) {
	retired := slices.Clone(s.retiredEnvs())
	for i := range retired {
		if sameEnvInstance(retired[i].EnvContainer, container) {
			update(&retired[i])
		}
	}
	s.gs.SetValue(globalStateKeyRetiredEnvs, retired)
}

// unretire drops the retired entry of an env the flow runs on again, since
// it is now cleaned up as the active env.
func (s flowEnvState) unretire(container env.EnvContainer) {
	retired := s.retiredEnvs()
	isInstance := func(r retiredEnv) bool { return sameEnvInstance(r.EnvContainer, container) }
	if !slices.ContainsFunc(retired, isInstance) {
		return
	}
	s.gs.SetValue(globalStateKeyRetiredEnvs, slices.DeleteFunc(slices.Clone(retired), isInstance))
}

// sameEnvInstance reports whether two containers refer to the same physical
// env. Sandboxes are matched by name alone, since a sandbox is also recorded
// at its base repo directory while its worktree is being provisioned.
func sameEnvInstance(a, b env.EnvContainer) bool {
	if a.Env.GetType() != b.Env.GetType() {
		return false
	}
	sandboxA, okA := a.Env.(env.SandboxEnv)
	sandboxB, okB := b.Env.(env.SandboxEnv)
	if okA && okB {
		return sandboxA.GetSandboxName() == sandboxB.GetSandboxName()
	}
	return env.InstanceOf(a) == env.InstanceOf(b)
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
