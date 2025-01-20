<template>
  <TaskModal v-if="isModalOpen" @close="closeModal" @created="refresh" @updated="refresh" :task="newTask" />
  <div v-if="showGuidedOverlay" class="guided-overlay">
    <div class="guided-text">
      Get started by adding your first task to the AI Sidekick queue!
    </div>
  </div>
  <div class="kanban-board">
    <div
      v-for="agentType in ['human', 'llm', 'none'] as const"
      :key="agentType"
      class="kanban-column"
    >
      <h2>
        {{ columnNames[agentType as keyof typeof columnNames] }}
        <button v-if="agentType !== 'none'" class="new-task mini-button" @click="addTask(agentType)">+</button>
        <button v-if="agentType === 'none' && groupedTasks[agentType]?.length > 0" class="new-task mini-button" @click="confirmArchiveFinished">📦</button>
      </h2>
      <TaskCard v-for="task in groupedTasks[agentType]" :key="task.id" :task="task" @deleted="refresh" @canceled="refresh" @archived="refresh" @updated="refresh" @error="error" />
      <button class="new-task" v-if="agentType == 'human'" @click="addTask(agentType)">+ Draft Task</button>
      <button class="new-task" v-if="agentType == 'llm'" @click="addTask(agentType)">+ Queue Task</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import type { FullTask, AgentType, Task } from '../lib/models'
import TaskCard from './TaskCard.vue'
import TaskModal from './TaskModal.vue'

const props = defineProps<{
  workspaceId: string,
  tasks: FullTask[],
  showGuidedOverlay: boolean
}>()

const columnNames = {
  human: 'You',
  llm: 'AI Sidekick',
  none: 'Finished',
}

const emit = defineEmits(['refresh', 'dismissOverlay'])

const groupedTasks = computed(() => {
  return props.tasks
    .reduce((grouped, task) => {
      grouped[task.agentType] = [...(grouped[task.agentType] || []), task];
      // sort by updated descending, if same then sort by id descending
      grouped[task.agentType].sort((a: FullTask, b: FullTask) => {
        if (b.updated === a.updated) {
          return b.id > a.id ? 1 : -1;
        }
        return b.updated > a.updated ? 1 : -1;
      });
      return grouped;
    }, {} as Record<AgentType, FullTask[]>);
})


function refresh() {
  emit('refresh');
}

const isModalOpen = ref(false)
const newTask = ref<Task>({
  status: 'drafting',
  agentType: 'human',
  workspaceId: props.workspaceId,
})

const addTask = (agentType: 'human' | 'llm' | 'none') => {
  if (agentType !== 'none') {
    isModalOpen.value = true
    newTask.value.agentType = agentType
    newTask.value.status = agentType === 'human' ? 'drafting' : 'to_do';
    if (agentType === 'llm' && props.showGuidedOverlay) {
      emit('dismissOverlay')
    }
  }
}

const closeModal = () => {
  isModalOpen.value = false
  newTask.value = {
    status: 'drafting',
    agentType: 'human',
    workspaceId: props.workspaceId,
  }
}

function error(e: any) {
  // TODO /gen/req/planned use a custom component here and on any other uses of
  // alert and some uses of console.error (when in response to specific user
  // action like clicking buttons) in the frontend directory
  alert(e)
}

async function confirmArchiveFinished() {
  if (confirm('Are you sure you want to archive all finished tasks?')) {
    try {
      const response = await fetch(`/api/v1/workspaces/${props.workspaceId}/tasks/archive_finished`, { method: 'POST' });
      if (!response.ok) {
        throw new Error('Failed to archive finished tasks');
      }
      refresh();
    } catch (e) {
      error(e);
    }
  }
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

.kanban-column:hover .new-task.mini-button {
  opacity: 1.0;
}

.new-task:hover {
  border-color: rgba(255, 255, 255, 0.02);
  background-color: rgba(255, 255, 255, 0.07);
}

.guided-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: rgba(0, 0, 0, 0.85);
  z-index: 99999;
}

.guided-overlay::before {
  content: '';
  position: absolute;
  top: 50%;
  left: 66.6%;
  transform: translate(-50%, calc(-50% + 7rem));
  width: 14rem;
  height: 4rem;
  background: radial-gradient(
    circle at center,
    transparent 0%,
    transparent 40%,
    rgba(0, 0, 0, 0.85) 100%
  );
  filter: blur(0.5rem);
}

.guided-text {
  position: absolute;
  top: 50%;
  left: 66.6%;
  transform: translate(-50%, calc(-50% + 2rem));
  color: var(--color-text);
  font-size: 1.2rem;
  text-align: center;
  width: 20rem;
  padding: 1.5rem;
  background: var(--color-background);
  border: 1px solid var(--color-border);
  border-radius: 0.5rem;
  box-shadow: 0 0.5rem 2rem rgba(0, 0, 0, 0.25);
}
</style>