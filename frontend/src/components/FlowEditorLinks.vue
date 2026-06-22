<template>
  <div class="editor-links">
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
import type { Worktree } from '@/lib/models'

const props = defineProps<{
  flowId: string
  worktrees?: Worktree[] | null
}>()

const worktrees = computed(() => props.worktrees ?? [])
const devMode = import.meta.env.MODE === 'development'
</script>

<style scoped>
.editor-links {
  position: absolute;
  z-index: 1000;
  top: 1rem;
  right: 1rem;
}

.editor-links a > * {
  height: 1.2rem;
  vertical-align: middle;
}
</style>