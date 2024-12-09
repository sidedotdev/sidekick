<template>
  <div :class="`task-card ${task.status.toLowerCase()}`" @click="cardClicked">
    <div class="actions">
      <button v-if="task.status == 'drafting'" class="action edit" @click.stop="openEditModal">✎️</button>
      <button class="action delete" @click.stop="deleteTask">X</button>
    </div>

    <h3>{{ task.title }}</h3>
    <p>{{ task.description }}</p>
    <span :class="`status-label ${task.status.toLowerCase()}`">{{ statusLabel(task.status) }}</span>
  </div>

  <TaskEditModal v-if="isEditModalOpen" :task="task" @close="closeEditModal" @updated="onUpdated" />
</template>

<script setup lang="ts">
import { ref } from 'vue'
import type { Task } from '../lib/models'
import TaskEditModal from './TaskEditModal.vue'
import router from '@/router'

const props = defineProps({
  task: {
    type: Object as () => Task,
    required: true,
  },
})

interface Emits {
  (event: 'deleted', id: string): void;
  (event: 'updated', id: string): void;
  (event: 'error', message: string): void;
}

const emit = defineEmits<Emits>();

const statusLabel = (status: string) => {
  switch (status) {
    case 'drafting':
      return 'Drafting';
    case 'to_do':
      return 'To Do';
    case 'blocked':
      return 'Blocked';
    case 'failed':
      return 'Failed';
    case 'in_progress':
      return 'In Progress';
    case 'complete':
      return 'Complete';
    default:
      return '';
  }
};


const isEditModalOpen = ref(false);

const openEditModal = () => {
  isEditModalOpen.value = true
}

const closeEditModal = () => {
  isEditModalOpen.value = false
}

const deleteTask = async () => {
  if (!window.confirm('Are you sure you want to delete this task?')) {
    return
  }

  const {id, workspaceId} = props.task
  const response = await fetch(`/api/v1/workspaces/${workspaceId}/tasks/${id}`, {
    method: 'DELETE',
  })
  if (response.status === 200) {
    // Remove the task from the list
    emit('deleted', id)
  } else {
    // Show an error message to the user
    emit('error', 'Failed to delete task')
  }
}

const cardClicked = async () => {
  // check if there is an active text selection
  const selection = window.getSelection()?.toString();
  if (selection) {
    // don't perform any action if there is an active text selection, as they
    // might be trying to copy text
    return
  }

  if (props.task.flows && props.task.flows.length > 0) {
    const firstFlowId = props.task.flows[0].id
    // Navigate to the first flow using vue-router
    // Replace 'flow' with the actual route name for the flow component
    router.push({ name: 'flow', params: { id: firstFlowId } })
  } else {
    // Edit the task
    openEditModal()
  }
}

const onUpdated = async () => {
  emit('updated', props.task.id)
}
</script>


<style scoped>
/* dark mode */
.task-card {
  --task-card-border: #454545;
  --task-card-background: rgba(255, 255, 255, 0.07);
  --task-card-hover-background: rgba(255, 255, 255, 0.15);
  --status-label-color: white;
  --action-background: #1e1e1e;
  --action-color: white;
  --action-box-shadow: 0 0 1px rgba(0, 0, 0, 0.9);
}

@media (prefers-color-scheme: light) {
  .task-card {
    --task-card-border: #ddd;
    --task-card-background: var(--color-background-soft);
    --task-card-hover-background: var(--color-background);
    --status-label-color: black;
    --action-background: var(--color-background-mute);
    --action-color: black;
    --action-box-shadow: 0 0 1px rgba(0, 0, 0, 0.1);
  }
}

.task-card {
  border: 1px solid var(--task-card-border);
  background-color: var(--task-card-background);
  border-radius: 2px;
  padding: var(--task-pad) calc(var(--task-pad) / 2);
  margin-top: var(--kanban-gap);
  transition: box-shadow 0.3s ease;
  font-family: sans-serif;
}

.task-card:hover {
  box-shadow: 0 2px 5px var(--action-box-shadow);
  background-color: var(--task-card-hover-background);
  cursor: pointer;
}

.status-label {
  padding: 0px 7px;
  border-radius: 1px;
  font-size: 1em;
  text-transform: capitalize;
  font-size: 13px;
  font-weight: 600;
  text-shadow: 1px 1px rgba(255, 255, 255, 0.1);
  color: var(--status-label-color);
  font-family: "JetBrains Mono", monospace;
}

.status-label.drafting {
  background-color: #626262;
}
.status-label.to_do {
  background-color: #a3a3a3;
}
.status-label.blocked {
  background-color: #ff8e42;
}

.status-label.failed {
  background-color: #ff4000;
}

.status-label.in_progress {
  background-color: #03a9f4;
}

.status-label.complete {
  background-color: #4caf50;
}

.task-card p {
  /* NOTE: doing line-clamp messes up the ::first-line style in chrome (not
   * firefox), so preferring that instead. ideally we'd have both. */
  /*
  -webkit-box-orient: vertical;
  box-orient: vertical;
  display: -webkit-box;
  -webkit-line-clamp: 5;
  line-clamp: 5;
  */
  overflow: hidden;
  white-space: pre-wrap;
  word-wrap: break-word;
  max-height: 10rem;
  margin-top: -10px;
  margin-bottom: 8px;
  max-height: 10rem;
}
.task-card:hover p {
  max-height: none;
  -webkit-line-clamp: 1000;
  line-clamp: 1000;
  overflow: visible;
  max-height: none;
  z-index: 1;
}
.task-card p::first-line {
  font-size: 1.0rem;
  line-height: 2.0;
}

.task-card a {
  display: block;
  margin-top: 1rem;
}

.task-card {
  position: relative;
}
.actions {
  position: absolute;
  right: calc(var(--task-pad) / 2);
  top: calc(var(--task-pad) / 2);
  display: flex;
}
.task-card:hover .action {
  visibility: visible;
  opacity: 1;
}
.action {
  color: var(--action-color);
  background-color: var(--action-background);
  padding: 5px 10px;
  border: 0;
  box-shadow: var(--action-box-shadow);
  visibility: hidden;
  opacity: 0.0;
  transition: opacity 0.2s;
  font-weight: 200;
}
.action:hover {
  background-color: var(--color-background-hover);
}
.action:first-child {
  border-top-left-radius: 5px;
  border-bottom-left-radius: 5px;
}
.action:last-child {
  border-top-right-radius: 5px;
  border-bottom-right-radius: 5px;
}
</style>
