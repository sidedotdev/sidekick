<template>
  <div class="kanban-column-group" :class="{ headingless: !showHeadings }">
    <div
      v-for="agentType in ['human', 'llm', 'none'] as const"
      :key="agentType"
      class="kanban-column"
    >
      <h2 v-if="showHeadings">
        {{ columnNames[agentType] }}
        <button v-if="agentType !== 'none'" class="new-task mini-button" @click="emit('addTask', agentType)">+</button>
        <button v-if="agentType === 'none' && groupedTasks[agentType]?.length > 0" class="new-task mini-button" @click="emit('archiveFinished')">📦</button>
      </h2>
      <VirtualTaskList
        :tasks="groupedTasks[agentType] ?? []"
        draggable
        @deleted="emit('refresh')"
        @canceled="emit('refresh')"
        @archived="emit('refresh')"
        @updated="emit('refresh')"
        @edit="(task: FullTask) => emit('edit', task)"
        @copy="(task: Task) => emit('copy', task)"
        @error="(e: any) => emit('error', e)"
      />
      <button class="new-task" v-if="agentType == 'human'" @click="emit('addTask', agentType)">
        + Draft Task
        <ShortcutHint v-if="newTaskShortcutLabel" :label="newTaskShortcutLabel" />
      </button>
      <button class="new-task" v-if="agentType == 'llm'" @click="emit('addTask', agentType)">
        + Queue Task
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { FullTask, AgentType, Task } from '../lib/models'
import VirtualTaskList from './VirtualTaskList.vue'
import ShortcutHint from './ShortcutHint.vue'

const props = withDefaults(defineProps<{
  tasks: FullTask[]
  newTaskShortcutLabel?: string
  // When headings are rendered once by the surrounding board instead (project
  // groups), the per-column headings are omitted and the columns lose their
  // boxed styling; the add-task buttons remain visible in each column
  showHeadings?: boolean
}>(), {
  showHeadings: true,
})

const emit = defineEmits<{
  (e: 'addTask', agentType: AgentType): void
  (e: 'archiveFinished'): void
  (e: 'refresh'): void
  (e: 'edit', task: FullTask): void
  (e: 'copy', task: Task): void
  (e: 'error', err: unknown): void
}>()

const columnNames = {
  human: 'You',
  llm: 'AI Sidekick',
  none: 'Finished',
}

const groupedTasks = computed(() => {
  const grouped = props.tasks.reduce((acc, task) => {
    if (!acc[task.agentType]) {
      acc[task.agentType] = [];
    }
    acc[task.agentType].push(task);
    return acc;
  }, {} as Record<AgentType, FullTask[]>);

  for (const agentType in grouped) {
    grouped[agentType as AgentType].sort((a: FullTask, b: FullTask) => {
      if (b.updated === a.updated) {
        return b.id > a.id ? 1 : -1;
      }
      return b.updated > a.updated ? 1 : -1;
    });
  }

  return grouped;
})
</script>

<style scoped>
.kanban-column-group {
  display: flex;
  width: 100%;
  gap: var(--kanban-column-gap, 0.75rem);
}

/* Columns draw no borders or background of their own: the board renders
   continuous full-height backdrop strips behind them instead */
.kanban-column {
  flex: 1;
  width: 33.3%;
  padding: var(--kanban-gap);
  transition: box-shadow 0.3s ease;
  font-family: sans-serif;
  min-height: 25rem;
}

/* Inside project groups, columns size to their content */
.kanban-column-group.headingless .kanban-column {
  min-height: 0;
}

h2 {
  /* lines up with the task card padding */
  padding-left: calc(var(--task-pad) / 2);
  display: flex;
  flex-direction: row;
  align-items: baseline;
  justify-content: space-between;
  font-weight: 400;
  font-size: 1.2rem;
}

.new-task {
  font-family: "JetBrains Mono", monospace;
  margin-top: calc(var(--kanban-gap) / 2);
  padding: calc(0.3125rem + var(--task-pad) / 2) calc(var(--task-pad) / 2);
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 1.0rem;
  line-height: 1.0;
  background: transparent;
  border: 1px solid transparent;
  border-radius: 0.3125rem;
  width: 100%;
  text-align: left;
  color: var(--color-text);
}

.new-task.mini-button {
  font-size: 1.5rem;
  font-weight: 200;
  padding: 0.25rem 0.5rem 0.4rem;
  margin-top: 0;
  margin-right: 0;
  width: 2.1875rem;
  height: 2.1875rem;
  text-align: center;
  line-height: 0.8;
  display: flex;
  align-items: center;
  justify-content: center;
  opacity: 0.0;
  transition: opacity 0.2s;
}

.kanban-column:hover .new-task.mini-button {
  opacity: 1.0;
}

.new-task:hover {
  border-color: rgba(255, 255, 255, 0.02);
  background-color: rgba(255, 255, 255, 0.07);
}

.new-task :deep(.shortcut-hint) {
  opacity: 0;
  transition: opacity 0.2s;
}

.new-task:hover :deep(.shortcut-hint) {
  opacity: 0.7;
}
</style>