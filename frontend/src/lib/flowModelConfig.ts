const MODEL_CONFIG_FLOW_STATUSES = new Set(['started', 'paused'])

export const isFlowModelConfigVisible = (status: string, devMode: boolean): boolean =>
  devMode && MODEL_CONFIG_FLOW_STATUSES.has(status)