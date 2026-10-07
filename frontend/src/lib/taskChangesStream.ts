import { ref, type Ref } from 'vue'
import type { FullTask } from './models'
import { viewCache } from './viewCache'

const RECONNECT_DELAY_MS = 1000

const parseTaskDates = (task: any): FullTask => {
  if (task.created) task.created = new Date(task.created)
  if (task.updated) task.updated = new Date(task.updated)
  if (task.archived) task.archived = new Date(task.archived)
  return task as FullTask
}

/**
 * Keeps a workspace's task list in sync via the task changes websocket. It
 * outlives the kanban view so that navigating into a flow and back doesn't
 * drop the connection or require catching up from scratch.
 */
export class TaskChangesStream {
  readonly tasks: Ref<FullTask[]>
  private socket: WebSocket | null = null
  private closed = false
  private lastTaskStreamId: string | null = null
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null

  constructor(readonly workspaceId: string) {
    this.tasks = ref(viewCache.getKanbanTasks(workspaceId) ?? [])
  }

  fetchTasks = async () => {
    try {
      const response = await fetch(`/api/v1/workspaces/${this.workspaceId}/tasks`)
      const data = await response.json()
      if (this.closed) return
      this.tasks.value = data.tasks.map((task: any) => parseTaskDates(task))
      viewCache.setKanbanTasks(this.workspaceId, this.tasks.value)
    } catch (error) {
      console.error('Failed to fetch tasks:', error)
    }
  }

  connect() {
    const streamIdParam = this.lastTaskStreamId ? `?lastTaskStreamId=${this.lastTaskStreamId}` : ''
    const socket = new WebSocket(
      `ws://${window.location.host}/ws/v1/workspaces/${this.workspaceId}/task_changes${streamIdParam}`
    )
    this.socket = socket

    socket.onopen = () => {
      this.fetchTasks()
    }

    socket.onmessage = (event) => {
      const data = JSON.parse(event.data)
      this.lastTaskStreamId = data.lastTaskStreamId
      this.mergeTasks(data.tasks)
    }

    socket.onerror = (error) => {
      console.error('Task changes websocket error:', error)
    }

    socket.onclose = (event) => {
      if (this.closed || this.socket !== socket) return
      console.log('Task changes websocket closed, reconnecting in 1 second.', event.reason)
      this.reconnectTimer = setTimeout(() => {
        this.reconnectTimer = null
        this.connect()
      }, RECONNECT_DELAY_MS)
    }
  }

  close() {
    this.closed = true
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer)
      this.reconnectTimer = null
    }
    this.socket?.close()
    this.socket = null
  }

  private mergeTasks(newTasks: FullTask[]) {
    newTasks.forEach(task => {
      const parsedTask = parseTaskDates(task)
      const index = this.tasks.value.findIndex(t => t.id === parsedTask.id)
      if (index !== -1) {
        this.tasks.value.splice(index, 1, parsedTask)
      } else {
        this.tasks.value.push(parsedTask)
      }
    })
    viewCache.setKanbanTasks(this.workspaceId, this.tasks.value)
  }
}

let activeStream: TaskChangesStream | null = null

/** Returns the shared stream for the workspace, replacing any stream for a different workspace. */
export const getTaskChangesStream = (workspaceId: string): TaskChangesStream => {
  if (activeStream?.workspaceId === workspaceId) return activeStream
  activeStream?.close()
  activeStream = new TaskChangesStream(workspaceId)
  activeStream.connect()
  return activeStream
}

export const closeTaskChangesStream = () => {
  activeStream?.close()
  activeStream = null
}