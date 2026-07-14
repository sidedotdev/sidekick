import type { FlowStatus } from './models'

const MODEL_CONFIG_FLOW_STATUSES = new Set<FlowStatus>(['in_progress', 'paused'])

export const isFlowModelConfigVisible = (status: FlowStatus, devMode: boolean): boolean => {
  return devMode && MODEL_CONFIG_FLOW_STATUSES.has(status)
}