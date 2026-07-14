<template>
  <div class="editor-links" :class="{ 'editor-links-subtask': subtask }">
    <div class="toolbar">
      <p v-for="worktree in worktrees" :key="worktree.id">
        Open Worktree
        <a :href="`vscode://file/${worktree.workingDirectory}?windowId=_blank`">
          <VSCodeIcon/>
        </a>&nbsp;<a :href="`idea://open?file=${encodeURIComponent(worktree.workingDirectory)}`">
          <IntellijIcon/>
        </a>&nbsp;<a :href="`zed://file/${worktree.workingDirectory}`">
          <ZedIcon/>
        </a>
      </p>
      <button
        v-if="showModelConfiguration"
        type="button"
        class="model-configuration-button"
        aria-label="Model configuration"
        title="Model configuration"
        @click="emit('model-configuration')"
      >
        <SlidersIcon />
      </button>
    </div>
    <div class="debug" v-if="devMode">
      <a :href="`http://localhost:19855/namespaces/default/workflows/${flowId}`">Temporal Flow</a>
      | <router-link :to="{ name: 'flow-reset', params: { id: flowId } }">Reset Workflow</router-link>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import VSCodeIcon from '@/components/icons/VSCodeIcon.vue'
import IntellijIcon from '@/components/icons/IntellijIcon.vue'
import ZedIcon from '@/components/icons/ZedIcon.vue'
import SlidersIcon from '@/components/icons/SlidersIcon.vue'
import type { Worktree } from '@/lib/models'

const props = defineProps<{
  flowId: string
  worktrees?: Worktree[] | null
  subtask?: boolean
  showModelConfiguration?: boolean
}>()

const emit = defineEmits<{
  (event: 'model-configuration'): void
}>()

const worktrees = computed(() => props.worktrees ?? [])
const devMode = import.meta.env.MODE === 'development'
</script>

<style scoped>
.editor-links {
  position: fixed;
  z-index: 1000;
  top: 1rem;
  right: 1rem;
  background-color: var(--color-background-soft);
}

/* When shown for a sub-task, the links overlay the side panel header, so they
   must sit above the panel (z-index 1001) and clear its dismiss control. */
.editor-links-subtask {
  z-index: 1002;
  right: 3rem;
}

.toolbar {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.toolbar p {
  margin: 0;
}

.model-configuration-button {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 2rem;
  height: 2rem;
  padding: 0.25rem;
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  background: var(--color-background-soft);
  color: var(--color-text);
  cursor: pointer;
}

.model-configuration-button:hover {
  border-color: var(--color-border-hover);
}

.editor-links a > * {
  height: 1.2rem;
  vertical-align: middle;
}
</style>
