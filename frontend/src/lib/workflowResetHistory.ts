export interface HistoryEvent {
  eventId: number
  eventType: string
  name: string
  timestamp: number
  groupId?: number
  failure?: {
    cause: string
    message: string
    stackTrace: string
  } | null
  resetBeforeEventId: number | null
  resetAfterEventId: number | null
}

export interface HistoryGroup extends HistoryEvent {
  events: HistoryEvent[]
  firstEventId: number
  lastEventId: number
  partial: boolean
}

export function groupWorkflowHistory(events: HistoryEvent[]): HistoryGroup[] {
  const before = new Map<number, number | null>()
  const after = new Map<number, number | null>()
  let boundary: number | null = null
  for (let i = events.length - 1; i >= 0; i--) {
    const event = events[i]
    before.set(event.eventId, boundary ?? event.resetBeforeEventId)
    if (event.eventType === 'WorkflowTaskCompleted') boundary = event.eventId
  }
  boundary = null
  const groups = new Map<number, HistoryGroup>()
  for (const event of events) {
    after.set(event.eventId, boundary ?? event.resetAfterEventId)
    if (event.eventType === 'WorkflowTaskCompleted') boundary = event.eventId
    const id = event.groupId || event.eventId
    let group = groups.get(id)
    if (!group) {
      group = {
        ...event,
        eventId: id,
        firstEventId: event.eventId,
        lastEventId: event.eventId,
        events: [],
        partial: false,
      }
      groups.set(id, group)
    }
    group.events.push(event)
    group.firstEventId = event.eventId
    if (event.name) group.name = event.name
  }
  for (const group of groups.values()) {
    group.partial = group.firstEventId !== group.eventId
    group.resetBeforeEventId = group.partial ? null : before.get(group.firstEventId) ?? null
    group.resetAfterEventId = after.get(group.lastEventId) ?? null
    group.events.reverse()
  }
  return [...groups.values()]
}