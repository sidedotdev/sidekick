import { DotReporter } from 'vitest/node'
import type { UserConsoleLog, File, Task } from 'vitest/node'

/**
 * Dot-style reporter that suppresses console output from passing tests.
 * Logs are buffered per-task and only printed for failing tests.
 */
export default class QuietReporter extends DotReporter {
  private logBuffer = new Map<string, UserConsoleLog[]>()

  onUserConsoleLog(log: UserConsoleLog) {
    const key = log.taskId ?? '__global__'
    let logs = this.logBuffer.get(key)
    if (!logs) {
      logs = []
      this.logBuffer.set(key, logs)
    }
    logs.push(log)
  }

  private collectFailedTaskIds(tasks: Task[]): Set<string> {
    const failed = new Set<string>()
    for (const task of tasks) {
      if (task.result?.state === 'fail') {
        failed.add(task.id)
      }
      if ('tasks' in task && Array.isArray(task.tasks)) {
        for (const id of this.collectFailedTaskIds(task.tasks)) {
          failed.add(id)
        }
      }
    }
    return failed
  }

  async onFinished(files?: File[], errors?: unknown[]) {
    const failedIds = this.collectFailedTaskIds(files ?? [])

    for (const [taskId, logs] of this.logBuffer) {
      if (failedIds.has(taskId)) {
        for (const log of logs) {
          super.onUserConsoleLog(log)
        }
      }
    }
    this.logBuffer.clear()

    await super.onFinished(files, errors)
  }
}