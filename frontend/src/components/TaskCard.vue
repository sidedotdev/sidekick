<template>
  <div :class="`task-card ${task.status.toLowerCase()}`" @click="cardClicked">
    <div class="actions">
      <button v-if="task.status == 'drafting'" class="action edit" title="Edit task" @click.stop="openEditModal">✎️</button>
      <button class="action copy" title="Duplicate task" @click.stop="copyTask"><CopyIcon/></button>
      <button v-if="canArchive" class="action archive" title="Archive task" @click.stop="archiveTask">📦</button>
      <button v-if="canCancel" class="action cancel" title="Cancel task" @click.stop="cancelTask">X</button>
      <button v-if="canDelete" class="action delete" title="Delete task" @click.stop="deleteTask"><TrashIcon/></button>
    </div>

    <h3>{{ task.title }}</h3>
    <p>{{ task.description }}</p>
    <span :class="`status-label ${task.status.toLowerCase()}`">{{ statusLabel(task.status) }}</span>
    <span v-if="task.archived" class="archived-label">Archived</span>

    <span v-if="llmPresetLabel" class="llm-preset-label">{{ llmPresetLabel }}</span>
  </div>

  <TaskModal v-if="isCopyModalOpen" :task="copiedTask" @close="closeCopyModal" @updated="onUpdated" />
  <TaskModal v-if="isEditModalOpen" :task="task" @close="closeEditModal" @updated="onUpdated" />
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import type { FullTask, Task, LLMConfig } from '../lib/models'
import { getModelSummary } from '../lib/llmPresets'
import { loadPresets, llmConfigsEqual } from '../lib/llmPresetStorage'
import TaskModal from './TaskModal.vue'
import CopyIcon from './icons/CopyIcon.vue'
import TrashIcon from './icons/TrashIcon.vue'
import router from '@/router'

const props = defineProps({
  task: {
    type: Object as () => FullTask,
    required: true,
  },
})

const llmPresetLabel = computed(() => {
  const llmOverride = props.task.flowOptions?.configOverrides?.llm as LLMConfig | undefined
  if (!llmOverride) return ''

  const presets = loadPresets()
  const match = presets.find(p => llmConfigsEqual(p.config, llmOverride))
  if (match) {
    const name = (match.name || '').trim()
    return name ? name : getModelSummary(match.config)
  }

  return getModelSummary(llmOverride)
})

const copiedTask = computed(() => {
  const copy: Task = {
    title: props.task.title,
    description: props.task.description,
    workspaceId: props.task.workspaceId,
    flowType: props.task.flowType,
    flowOptions: props.task.flowOptions,
    status: props.task.status,
    agentType: 'llm',
  }

  if (copy.status !== 'drafting' && copy.status !== 'to_do') {
    copy.status = 'to_do'
  }

  delete copy.id
  delete copy.flows
  delete copy.updated
  delete copy.created
  return copy
})

interface Emits {
  (event: 'deleted', id: string): void;
  (event: 'updated', id: string): void;
  (event: 'error', message: string): void;
  (event: 'archived', id: string): void;
  (event: 'canceled', id: string): void;
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
    case 'canceled':
      return 'Canceled';
    case 'failed':
      return 'Failed';
    case 'in_progress':
      return 'In Progress';
    case 'in_review':
      return 'In Review';
    case 'complete':
      return 'Complete';
    default:
      return '';
  }
};

const canArchive = computed(() => ['complete', 'failed', 'canceled'].includes(props.task.status) && !props.task.archived);
const canDelete = computed(() => props.task.status === 'drafting' || props.task.archived);
const canCancel = computed(() => ['to_do', 'in_progress', 'blocked', 'in_review'].includes(props.task.status) && !props.task.archived);

const isEditModalOpen = ref(false);
const isCopyModalOpen = ref(false);

const openEditModal = () => {
  isEditModalOpen.value = true
}

const closeEditModal = () => {
  isEditModalOpen.value = false
}

const closeCopyModal = () => {
  isCopyModalOpen.value = false
}


const archiveTask = async () => {
  const {id, workspaceId} = props.task
  const response = await fetch(`/api/v1/workspaces/${workspaceId}/tasks/${id}/archive`, {
    method: 'POST',
  })
  if (response.status === 204) {
    emit('archived', id)
    emit('updated', id)
  } else {
    emit('error', 'Failed to archive task')
  }
}

const deleteTask = async () => {
  if (!canDelete.value) {
    emit('error', 'This task cannot be deleted')
    return
  }

  if (!window.confirm('Are you sure you want to delete this task?')) {
    return
  }

  const {id, workspaceId} = props.task
  const response = await fetch(`/api/v1/workspaces/${workspaceId}/tasks/${id}`, {
    method: 'DELETE',
  })
  if (response.status === 200) {
    emit('deleted', id)
  } else {
    emit('error', 'Failed to delete task')
  }
}

const cardClicked = async () => {
  const selection = window.getSelection()?.toString();
  if (selection) {
    return
  }

  if (props.task.flows && props.task.flows.length > 0) {
    const firstFlowId = props.task.flows[0].id
    router.push({ name: 'flow', params: { id: firstFlowId } })
  } else {
    openEditModal()
  }
}

const onUpdated = async () => {
  emit('updated', props.task.id)
}

const copyTask = async () => {
  isCopyModalOpen.value = true
}

const cancelTask = async () => {
  if (!canCancel.value) {
    emit('error', 'This task cannot be canceled')
    return
  }

  // Confirm with the user before canceling the task
  if (!window.confirm('Are you sure you want to cancel this task?')) {
    return
  }

  const {id, workspaceId} = props.task
  const response = await fetch(`/api/v1/workspaces/${workspaceId}/tasks/${id}/cancel`, {
    method: 'POST',
  })
  if (response.status === 200) {
    const data = await response.json()
    emit('canceled', id)
    emit('updated', id)
  } else {
    const errorData = await response.json()
    emit('error', errorData.error || 'Failed to cancel task')
  }
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
.status-label.canceled {
  background-color: rgb(147, 147, 0);
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

.status-label.in_review {
  background-color: var(--p-primary-color);
  color: var(--p-primary-contrast-color);
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

.action.cancel {
  font-weight: bold;
}

.action.copy,
.action.delete {
  display: flex;
  align-items: center;
  justify-content: center;
}

.archived-label {
  margin-left: 0.5rem;
  padding: 0px 7px;
  border-radius: 1px;
  font-size: 13px;
  font-weight: 600;
  background-color: #808080;
  color: var(--status-label-color);
  font-family: "JetBrains Mono", monospace;
}

.llm-preset-label {
  position: absolute;
  right: calc(var(--task-pad) / 2);
  bottom: calc(var(--task-pad) / 2);
  max-width: calc(100% - var(--task-pad));
  padding: 0.1rem 0.4rem;
  border-radius: 0.2rem;
  font-size: 0.75rem;
  line-height: 1.2;
  opacity: 0.75;
  background-color: var(--color-background-mute);
  color: var(--color-text);
  pointer-events: none;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.action.copy,
.action.delete {
  color: #000;
}
.action.copy svg,
.action.delete svg {
  width: 0.8rem;
  height: 0.8rem;
}
</style>
