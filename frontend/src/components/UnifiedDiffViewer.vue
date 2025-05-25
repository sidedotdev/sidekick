<template>
  <div class="unified-diff-viewer">
    <div v-if="parsedFiles.length === 0" class="no-diff-message">
      No diff content to display
    </div>
    <DiffFile
      v-for="(fileData, index) in parsedFiles"
      :key="`${fileData.oldFile.fileName || 'unknown'}-${fileData.newFile.fileName || 'unknown'}-${index}`"
      :file-data="fileData"
      :default-expanded="defaultExpanded"
    />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import DiffFile from './DiffFile.vue'
import { parseDiff } from '../lib/diffUtils'

interface Props {
  diffString: string
  defaultExpanded?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  defaultExpanded: false
})

const parsedFiles = computed(() => {
  if (!props.diffString || props.diffString.trim() === '') {
    return []
  }
  
  try {
    return parseDiff(props.diffString)
  } catch (error) {
    console.error('Failed to parse diff string:', error)
    return []
  }
})
</script>

<style scoped>
.unified-diff-viewer {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  margin: 0.5rem 0 0;
}

.no-diff-message {
  padding: 1rem;
  text-align: center;
  color: var(--color-text-muted);
  font-style: italic;
  background: var(--color-background-soft);
  border: 1px solid var(--color-border);
  border-radius: 0.375rem;
}
</style>