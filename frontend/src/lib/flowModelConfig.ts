const MODEL_CONFIG_FLOW_STATUSES = new Set(['in_progress', 'paused'])

export const isFlowModelConfigVisible = (status: string, devMode: boolean): boolean => {
  console.log({status})
  return devMode && MODEL_CONFIG_FLOW_STATUSES.has(status)
}