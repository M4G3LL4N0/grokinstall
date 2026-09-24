package registry

// State is the lifecycle state of an installed capability. It is deliberately
// explicit: a plan is not an installed capability, and a capability whose files
// are inconsistent is never presented as ready.
type State string

// Capability states.
const (
	// StateReady means the capability is installed and verified runnable.
	StateReady State = "ready"
	// StateBroken means the capability is installed but no longer works.
	StateBroken State = "broken"
	// StateDirty means an install mutation partially applied and automatic
	// rollback could not prove complete restoration.
	StateDirty State = "dirty"
	// StateUninstalled records a capability that was removed.
	StateUninstalled State = "uninstalled"
	// StatePlanOnly exists only on persisted plans, never on installed entries.
	StatePlanOnly State = "plan_only"
)

// Installed reports whether a state represents something registered as a
// capability.
func (s State) Installed() bool {
	return s == StateReady || s == StateBroken || s == StateDirty
}

// Runnable reports whether a state should be offered to GrokBot for calling.
func (s State) Runnable() bool { return s == StateReady }

// Validate rejects unknown states.
func (s State) Validate() bool {
	switch s {
	case StateReady, StateBroken, StateDirty, StateUninstalled, StatePlanOnly:
		return true
	}
	return false
}
