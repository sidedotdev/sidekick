<template>
  <div class="task-card-shell">
    <div :class="['task-card', task.status.toLowerCase(), { 'has-title': task.title }]" @click="cardClicked">
      <div class="actions">
        <button v-if="task.status == 'drafting'" class="action edit" title="Edit task" @click.stop="openEditModal">✎️</button>
        <button class="action copy" title="Duplicate task" @click.stop="copyTask"><CopyIcon/></button>
        <button v-if="canArchive" class="action archive" title="Archive task" @click.stop="archiveTask">📦</button>
        <button v-if="canCancel" class="action cancel" title="Cancel task" @click.stop="cancelTask">X</button>
        <button v-if="canDelete" class="action delete" title="Delete task" @click.stop="deleteTask"><TrashIcon/></button>
      </div>

      <h3 v-if="task.title" class="task-title">{{ task.title }}</h3>
      <p class="task-description" @mouseleave.self="handleDescriptionBlur">{{ task.description }}</p>
      <div class="card-footer">
        <span :class="`status-label ${task.status.toLowerCase()}`">{{ statusLabel(task.status) }}</span>
        <span v-if="task.archived" class="archived-label">Archived</span>
      </div>

      <div v-if="envIndicator || llmPresetLabel" class="card-meta">
        <span v-if="envIndicator" class="env-indicator" :title="envIndicator.title">
          <component :is="envIndicator.icon"/>
        </span>
        <span v-if="llmPresetLabel" class="llm-preset-label">{{ llmPresetLabel }}</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, type Component } from 'vue'
import type { FullTask, Task, LLMConfig } from '../lib/models'
import { getModelSummary } from '../lib/llmPresets'
import { loadPresets, llmConfigsEqual } from '../lib/llmPresetStorage'
import CopyIcon from './icons/CopyIcon.vue'
import TrashIcon from './icons/TrashIcon.vue'
import ContainerIcon from './icons/ContainerIcon.vue'
import CloudIcon from './icons/CloudIcon.vue'
import router from '@/router'

const props = defineProps({
  task: {
    type: Object as () => FullTask,
    required: true,
  },
})

type EnvIndicator = { title: string; icon: Component }

// Maps concrete env types to an icon conveying where the task executes, with
// the tooltip carrying the specifics. Only env types that deviate from the
// default (local machine) are listed, so unmapped types (local,
// local_git_worktree, unknown) render no indicator.
const envIndicatorMap: Record<string, EnvIndicator> = {
  devpod: { title: 'DevPod container', icon: ContainerIcon },
  openshell: { title: 'OpenShell container', icon: ContainerIcon },
  modal: { title: 'Modal cloud sandbox', icon: CloudIcon },
}

const envIndicator = computed<EnvIndicator | null>(() => {
  const envType = props.task.flowOptions?.envType as string | undefined
  if (!envType) return null
  return envIndicatorMap[envType] ?? null
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
    projectId: props.task.projectId,
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
  (event: 'edit', task: FullTask): void;
  (event: 'copy', task: Task): void;
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

// Edit/copy modals are owned by an ancestor (e.g. the kanban board) so that
// background task updates that remount this card can't close an open modal.
const openEditModal = () => {
  emit('edit', props.task)
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
    // A task's flows may include sub-task flows (e.g. IDD sub-tasks share the
    // task as parent), so pick the flow matching the task's flow type instead
    // of relying on ordering.
    const flows = props.task.flows
    const targetFlow = flows.find((f) => f.type === props.task.flowType) ?? flows[0]
    const routeName = props.task.flowType === 'idd' ? 'intent-canvas' : 'flow'
    router.push({ name: routeName, params: { id: targetFlow.id } })
  } else {
    openEditModal()
  }
}

const copyTask = () => {
  emit('copy', copiedTask.value)
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

const handleDescriptionBlur = (event: FocusEvent) => {
  // we switch to scrolling overflow and back to hidden, but it can stay
  // scrolled which is very odd looking
  ;(event.target as HTMLElement).scrollTop = 0
}
</script>


<style scoped>
/* dark mode */
.task-card {
  --task-card-border: #454545;
  --task-card-background: color-mix(in srgb, white 7%, var(--color-background));
  --task-card-hover-background: color-mix(in srgb, white 15%, var(--color-background));
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

/* Keeps virtualized row measurements stable while the hovered card expands. */
.task-card-shell {
  position: relative;
  height: 7.5rem;
}

.task-card {
  border: 1px solid var(--task-card-border);
  background-color: var(--task-card-background);
  border-radius: var(--kanban-radius);
  padding: calc(var(--task-pad) / 2);
  transition: box-shadow 0.3s ease;
  font-family: sans-serif;
  height: 100%;
  overflow: hidden;
  position: relative;
}

.task-card:hover {
  box-shadow: 0 2px 5px var(--action-box-shadow);
  background-color: var(--task-card-hover-background);
  cursor: pointer;
  position: absolute;
  inset: 0 0 auto 0;
  height: auto;
  min-height: 100%;
  /* room for the absolutely positioned footer/meta row */
  padding-bottom: calc(var(--task-pad) / 2 + 1.5rem);
  z-index: 10;
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

.task-title {
  margin: 0 0 0.25rem 0;
  font-size: 1.05rem;
  line-height: 1.3;
  overflow: hidden;
  text-overflow: ellipsis;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.task-card:hover .task-title {
  display: block;
  -webkit-line-clamp: unset;
  overflow: visible;
}

.task-description {
  overflow: hidden;
  word-wrap: break-word;
  margin: 0 0 0.5rem 0;
  white-space: pre-wrap;
  font-size: 0.85;
  line-height: 1.4;
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
}

.task-card:not(.has-title) .task-description {
    padding-top: 0
}

/* When no title, make the first line title-sized */
.task-card:not(.has-title) .task-description::first-line {
  font-size: 1.05rem;
  line-height: 2.0;
}

.task-card:hover .task-description {
  display: block;
  -webkit-line-clamp: unset;
  /* The half line makes it obvious that overflowing text was cut off. */
  max-height: 18.5lh;
  /* TODO: only engage scroll capture when user starts scrolling within this element */
  overflow-y: auto;
}

.card-footer {
  position: absolute;
  bottom: calc(var(--task-pad) / 2);
  left: calc(var(--task-pad) / 2);
}

.task-card a {
  display: block;
  margin-top: 1rem;
}
.actions {
  position: absolute;
  right: calc(var(--task-pad) / 2);
  top: calc(var(--task-pad) / 2);
  display: flex;
  z-index: 2;
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

.card-meta {
  position: absolute;
  right: calc(var(--task-pad) / 2);
  bottom: calc(var(--task-pad) / 2);
  max-width: calc(100% - var(--task-pad));
  display: flex;
  align-items: center;
  gap: 0.35rem;
}

.env-indicator {
  display: inline-flex;
  align-items: center;
  padding: 0.1rem 0.25rem;
  border-radius: 0.2rem;
  opacity: 0.75;
  background-color: var(--color-background-mute);
  color: var(--color-text);
}
.env-indicator svg {
  display: block;
  width: 0.9rem;
  height: 0.9rem;
}

.llm-preset-label {
  min-width: 0;
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
