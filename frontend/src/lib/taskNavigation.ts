import router from '@/router'
import type { FullTask } from './models'

/** Opens the task's own flow, returning false when the task has no flow yet. */
export const openTaskFlow = (task: FullTask): boolean => {
  const flows = task.flows
  if (!flows || flows.length === 0) return false
  // A task's flows may include sub-task flows (e.g. IDD sub-tasks share the
  // task as parent), so pick the flow matching the task's flow type instead
  // of relying on ordering.
  const targetFlow = flows.find((f) => f.type === task.flowType) ?? flows[0]
  const routeName = task.flowType === 'idd' ? 'intent-canvas' : 'flow'
  router.push({ name: routeName, params: { id: targetFlow.id } })
  return true
}