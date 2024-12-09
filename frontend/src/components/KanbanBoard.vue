<template>
  <TaskCreationModal v-if="isModalOpen" @close="closeModal" @created="refresh" :status="newTaskStatus" />
  <div class="kanban-board">
    <div
      v-for="agentType in ['human' as const, 'llm' as const, 'none' as const]"
      :key="agentType"
      class="kanban-column"
    >
      <h2>
        {{ columnNames[agentType] }}
        <button :class="{'invisible': agentType === 'none'}" class="new-task mini-button" @click="newTask(agentType)">+</button>
      </h2>
      <TaskCard v-for="task in groupedTasks()[agentType]" :key="task.id" :task="task" @deleted="refresh" @updated="refresh" @error="error" />
      <button class="new-task" v-if="agentType == 'human'" @click="newTask(agentType)">+ Draft Task</button>
      <button class="new-task" v-if="agentType == 'llm'" @click="newTask(agentType)">+ Queue Task</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import type { Task, AgentType } from '../lib/models'
import TaskCard from './TaskCard.vue'
import TaskCreationModal from './TaskCreationModal.vue'

const columnNames = {
  human: 'You',
  llm: 'AI Sidekick',
  none: 'Complete',
}

const props = defineProps({
  tasks: {
    type: Array as () => Task[],
    required: true,
  },
})

const emit = defineEmits(['refresh'])

const groupedTasks = () => {
  return [...props.tasks]
    .sort()
    .reverse()
    .reduce((grouped, task) => {
      grouped[task.agentType] = [...(grouped[task.agentType] || []), task];
      // sort by updated descending, if same then sort by id descending
      grouped[task.agentType].sort((a: Task, b: Task) => {
        if (b.updated === a.updated) {
          return b.id > a.id ? 1 : -1;
        }
        return b.updated > a.updated ? 1 : -1;
      });
      return grouped;
    }, {} as Record<AgentType, Task[]>);
}


function refresh() {
  emit('refresh');
}

const isModalOpen = ref(false)
const newTaskStatus = ref('to_do')

const newTask = (agentType: string) => {
  isModalOpen.value = true
  newTaskStatus.value = agentType === 'human' ? 'drafting' : 'to_do';
}

const closeModal = () => {
  isModalOpen.value = false
}

function error(e: any) {
  // TODO /gen/req/planned use a custom component here and on any other uses of
  // alert and some uses of console.error (when in response to specific user
  // action like clicking buttons) in the frontend directory
  alert(e)
}
</script>

<style scoped>
.kanban-board {
  display: flex;
  gap: 0;
  flex-wrap: wrap;
  /*font-family: 'Roboto', sans-serif;*/
  background-color: var(--color-background);
  transition: background-color 0.5s, color 0.5s;

  --color-column-background: #181818;
  margin-bottom: 2rem;
}
@media (prefers-color-scheme: light) {
  .kanban-board {
    --color-column-background: #e5e5e5;
  }
}

.kanban-column {
  flex: 1;
  width: 33.3%;
  border: 1px solid var(--color-border);
  background-color: var(--color-background);
  padding: var(--kanban-gap);
  transition: box-shadow 0.3s ease;
  font-family: sans-serif;
  min-height: 400px;
}
.kanban-column + .kanban-column {
  border-left: 0;
}

.kanban-column:hover .new-task.mini-button {
  opacity: 1.0;
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
  padding: calc(5px + var(--task-pad) / 2) calc(var(--task-pad) / 2);
  display: block;
  font-size: 1.0rem;
  line-height: 1.0;
  background: transparent;
  border: 1px solid transparent;
  border-radius: 5px;
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
  width: 35px;
  height: 35px;
  text-align: center;
  line-height: 0.8;
  display: flex;
  align-items: center;
  justify-content: center;
  opacity: 0.0;
  transition: opacity 0.2s;
}

.new-task:hover {
  border-color: rgba(255, 255, 255, 0.02);
  background-color: rgba(255, 255, 255, 0.07);
}
</style>