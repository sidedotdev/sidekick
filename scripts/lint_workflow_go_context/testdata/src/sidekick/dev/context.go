package dev

import "sidekick/flow_action"

type DevContext struct {
	flow_action.ExecContext
}

type DevActionContext struct {
	DevContext
}
