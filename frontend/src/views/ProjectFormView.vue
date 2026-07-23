<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import Button from 'primevue/button'
import Select from 'primevue/select'
import type { Project, ProjectPriority } from '@/lib/models'
import { allProjectPriorities } from '@/lib/models'
import { projectPriorityLabels, endOfBucketRank } from '@/lib/projects'
import { store } from '@/lib/store'

const route = useRoute()
const router = useRouter()

const projectId = computed(() => route.params.id as string | undefined)
const isEditMode = computed(() => !!projectId.value)

const title = ref('')
const description = ref('')
const priority = ref<ProjectPriority>('none')
const originalPriority = ref<ProjectPriority | null>(null)
const existingRank = ref('')
const projects = ref<Project[]>([])
const loading = ref(true)
const notFound = ref(false)
const saving = ref(false)
const error = ref<string | null>(null)

const priorityOptions = allProjectPriorities.map(p => ({
  label: projectPriorityLabels[p],
  value: p,
}))

onMounted(async () => {
  try {
    // The backend exposes no GET-by-id project endpoint (only list, create,
    // update, delete), so edit mode resolves the project from the list
    // response, which is also needed for rank computation on priority change.
    const response = await fetch(`/api/v1/workspaces/${store.workspaceId}/projects`)
    if (!response.ok) {
      throw new Error('Failed to fetch projects')
    }
    const data = await response.json()
    projects.value = data.projects ?? []
    if (isEditMode.value) {
      const project = projects.value.find(p => p.id === projectId.value)
      if (!project) {
        notFound.value = true
        return
      }
      title.value = project.title
      description.value = project.description ?? ''
      priority.value = project.priority
      originalPriority.value = project.priority
      existingRank.value = project.rank ?? ''
    }
  } catch (err) {
    error.value = 'Error loading project'
    console.error(err)
  } finally {
    loading.value = false
  }
})

const save = async () => {
  if (!title.value.trim()) {
    error.value = 'Title is required'
    return
  }
  saving.value = true
  error.value = null

  // keep the project's position within its bucket unless the priority
  // changed, in which case it moves to the end of the new bucket
  const keepRank = isEditMode.value && priority.value === originalPriority.value
  const rank = keepRank
    ? existingRank.value
    : endOfBucketRank(projects.value.filter(p => p.id !== projectId.value), priority.value)

  const url = isEditMode.value
    ? `/api/v1/workspaces/${store.workspaceId}/projects/${projectId.value}`
    : `/api/v1/workspaces/${store.workspaceId}/projects`

  try {
    const response = await fetch(url, {
      method: isEditMode.value ? 'PUT' : 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        title: title.value.trim(),
        description: description.value,
        priority: priority.value,
        rank,
      }),
    })
    if (!response.ok) {
      const body = await response.json().catch(() => null)
      throw new Error(body?.error || 'Failed to save project')
    }
    router.push({ name: 'projects' })
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to save project'
    console.error(err)
  } finally {
    saving.value = false
  }
}

const cancel = () => {
  router.push({ name: 'projects' })
}
</script>

<template>
  <div class="project-form-view">
    <h1>{{ isEditMode ? 'Edit Project' : 'New Project' }}</h1>
    <div v-if="loading">Loading...</div>
    <div v-else-if="notFound" class="error">Project not found.</div>
    <form v-else class="project-form" @submit.prevent="save">
      <div v-if="error" class="error">{{ error }}</div>
      <label for="project-title">Title</label>
      <input id="project-title" v-model="title" type="text" placeholder="Project title" />
      <label for="project-description">Description</label>
      <textarea
        id="project-description"
        v-model="description"
        rows="4"
        placeholder="Optional description"
      ></textarea>
      <label for="project-priority">Priority</label>
      <Select
        id="project-priority"
        v-model="priority"
        :options="priorityOptions"
        optionLabel="label"
        optionValue="value"
        class="priority-select"
      />
      <div class="actions">
        <Button type="submit" :label="isEditMode ? 'Save' : 'Create'" :disabled="saving" size="small" />
        <Button type="button" label="Cancel" severity="secondary" text size="small" @click="cancel" />
      </div>
    </form>
  </div>
</template>

<style scoped>
.project-form-view {
  padding: 1rem;
  max-width: 40rem;
}

.project-form {
  display: flex;
  flex-direction: column;
  gap: 0.4rem;
  margin-top: 1rem;
}

.project-form label {
  font-size: 0.85rem;
  font-weight: 600;
  margin-top: 0.6rem;
}

.project-form input,
.project-form textarea {
  background-color: var(--color-background);
  color: var(--color-text);
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  padding: 0.5rem;
  font-family: inherit;
  font-size: 0.95rem;
}

.project-form textarea {
  resize: vertical;
}

.priority-select {
  align-self: flex-start;
  min-width: 12rem;
}

.actions {
  display: flex;
  gap: 0.5rem;
  margin-top: 1rem;
}

.error {
  color: var(--color-error-text);
}
</style>