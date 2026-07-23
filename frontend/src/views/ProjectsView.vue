<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { RouterLink } from 'vue-router'
import Button from 'primevue/button'
import TrashIcon from '@/components/icons/TrashIcon.vue'
import type { Project, ProjectPriority } from '@/lib/models'
import { allProjectPriorities } from '@/lib/models'
import { projectPriorityLabels, computeBucketRank } from '@/lib/projects'
import { store } from '@/lib/store'

const projects = ref<Project[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const draggedId = ref<string | null>(null)

const buckets = computed(() =>
  allProjectPriorities
    .map(priority => ({
      priority,
      label: projectPriorityLabels[priority],
      projects: projects.value.filter(p => p.priority === priority),
    }))
    .filter(bucket => bucket.projects.length > 0)
)

const fetchProjects = async () => {
  try {
    const response = await fetch(`/api/v1/workspaces/${store.workspaceId}/projects`)
    if (!response.ok) {
      throw new Error('Failed to fetch projects')
    }
    const data = await response.json()
    projects.value = data.projects ?? []
    error.value = null
  } catch (err) {
    error.value = 'Error fetching projects'
    console.error(err)
  } finally {
    loading.value = false
  }
}

onMounted(fetchProjects)

const deleteProject = async (project: Project) => {
  if (!window.confirm(`Delete project "${project.title}"?`)) return
  try {
    const response = await fetch(`/api/v1/workspaces/${store.workspaceId}/projects/${project.id}`, {
      method: 'DELETE',
    })
    if (!response.ok) {
      throw new Error('Failed to delete project')
    }
    projects.value = projects.value.filter(p => p.id !== project.id)
  } catch (err) {
    error.value = 'Error deleting project'
    console.error(err)
  }
}

const onDragStart = (event: DragEvent, project: Project) => {
  draggedId.value = project.id
  if (event.dataTransfer) {
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', project.id)
  }
}

const bucketWithoutDragged = (priority: ProjectPriority) =>
  projects.value.filter(p => p.priority === priority && p.id !== draggedId.value)

const draggedProject = () => projects.value.find(p => p.id === draggedId.value)

// Drag and drop only reorders projects within their existing priority bucket;
// changing a project's priority is done via the edit page.
const onDropOnProject = async (target: Project) => {
  const dragged = draggedProject()
  if (!dragged || dragged.id === target.id || dragged.priority !== target.priority) {
    draggedId.value = null
    return
  }
  // dropping on a project inserts the dragged project right before it
  const index = bucketWithoutDragged(target.priority).findIndex(p => p.id === target.id)
  await moveDraggedTo(index)
}

const onDropOnBucket = async (priority: ProjectPriority) => {
  const dragged = draggedProject()
  if (!dragged || dragged.priority !== priority) {
    draggedId.value = null
    return
  }
  await moveDraggedTo(bucketWithoutDragged(priority).length)
}

const moveDraggedTo = async (index: number) => {
  const project = draggedProject()
  draggedId.value = null
  if (!project) return
  const rank = computeBucketRank(projects.value, project.id, index)
  try {
    const response = await fetch(`/api/v1/workspaces/${store.workspaceId}/projects/${project.id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        title: project.title,
        description: project.description ?? '',
        priority: project.priority,
        rank,
      }),
    })
    if (!response.ok) {
      throw new Error('Failed to reorder project')
    }
    await fetchProjects()
  } catch (err) {
    error.value = 'Error reordering project'
    console.error(err)
  }
}
</script>

<template>
  <div class="projects-view">
    <div class="header-row">
      <h1>Projects</h1>
      <RouterLink :to="{ name: 'project-new' }" class="new-project-link">
        <Button label="New Project" size="small" />
      </RouterLink>
    </div>
    <div v-if="loading">Loading...</div>
    <div v-else-if="error" class="error">{{ error }}</div>
    <div v-else-if="projects.length === 0" class="empty">No projects yet.</div>
    <div v-else class="buckets">
      <section
        v-for="bucket in buckets"
        :key="bucket.priority"
        class="priority-bucket"
        :data-priority="bucket.priority"
        @dragover.prevent
        @drop.prevent="onDropOnBucket(bucket.priority)"
      >
        <h2>{{ bucket.label }}</h2>
        <ul class="project-list">
          <li
            v-for="project in bucket.projects"
            :key="project.id"
            class="project-item"
            draggable="true"
            @dragstart="onDragStart($event, project)"
            @dragend="draggedId = null"
            @dragover.prevent
            @drop.prevent.stop="onDropOnProject(project)"
          >
            <div class="project-info">
              <RouterLink
                class="project-title"
                :to="{ name: 'project-edit', params: { id: project.id } }"
              >
                {{ project.title }}
              </RouterLink>
              <p v-if="project.description" class="project-description">{{ project.description }}</p>
            </div>
            <button
              class="delete-button"
              type="button"
              :aria-label="`Delete ${project.title}`"
              @click="deleteProject(project)"
            >
              <TrashIcon />
            </button>
          </li>
        </ul>
      </section>
    </div>
  </div>
</template>

<style scoped>
.projects-view {
  padding: 1rem;
  max-width: 50rem;
}

.header-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 1rem;
}

.new-project-link {
  text-decoration: none;
}

.error {
  color: var(--color-error-text);
  margin-bottom: 1rem;
}

.priority-bucket {
  margin-bottom: 1.5rem;
}

.priority-bucket h2 {
  font-size: 0.9rem;
  text-transform: uppercase;
  letter-spacing: 0.05em;
  opacity: 0.7;
  margin-bottom: 0.5rem;
}

.project-list {
  list-style: none;
  padding: 0;
  margin: 0;
  min-height: 1.5rem;
}

.project-item {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  padding: 0.6rem 0.8rem;
  margin-bottom: 0.4rem;
  background-color: var(--color-background);
  border: 1px solid var(--color-border);
  border-radius: 0.35rem;
  cursor: grab;
}

.project-title {
  color: var(--color-text);
  font-weight: 600;
  text-decoration: none;
}

.project-title:hover {
  text-decoration: underline;
}

.project-description {
  margin: 0.2rem 0 0;
  font-size: 0.85rem;
  opacity: 0.7;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 36rem;
}

.delete-button {
  background: none;
  border: none;
  padding: 0.2rem;
  color: var(--color-text);
  opacity: 0.5;
  cursor: pointer;
  display: inline-flex;
  align-items: center;
}

.delete-button:hover {
  opacity: 1;
}
</style>