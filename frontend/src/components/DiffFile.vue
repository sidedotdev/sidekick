<template>
  <div class="diff-file">
    <div class="file-header" @click="toggleExpanded">
      <div class="file-header-content">
        <div class="file-path-container">
          <span class="expand-icon" :class="{ expanded: isExpanded }">▶</span>
          <span class="file-path">{{ filePath }}</span>
          <button class="copy-button" @click.stop="copyFilePath" title="Copy file path">
            <CopyIcon />
          </button>
        </div>
        <div class="file-summary">
          <div class="visual-summary">
            <div 
              v-for="(square, index) in visualSummary" 
              :key="index"
              class="summary-square"
              :class="square.type"
            ></div>
          </div>
          <div class="line-counts">
            <span v-if="fileData.linesAdded > 0" class="added-count">+{{ fileData.linesAdded }}</span>
            <span v-if="fileData.linesRemoved > 0" class="removed-count">-{{ fileData.linesRemoved }}</span>
          </div>
        </div>
      </div>
    </div>
    <div v-if="isExpanded" class="diff-content">
      <DiffView
        :data="fileData"
        :diff-view-font-size="14"
        :diff-view-mode="viewMode"
        :diff-view-highlight="true"
        :diff-view-add-widget="false"
        :diff-view-wrap="true"
        :diff-view-theme="getTheme()"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import CopyIcon from './icons/CopyIcon.vue'
import type { ParsedDiff } from '../lib/diffUtils'
import "@git-diff-view/vue/styles/diff-view.css"
import { DiffView, DiffModeEnum } from "@git-diff-view/vue"

interface Props {
  fileData: ParsedDiff
  defaultExpanded?: boolean
  diffMode?: 'unified' | 'split'
  level?: number
}

const props = withDefaults(defineProps<Props>(), {
  defaultExpanded: false,
  diffMode: 'unified',
  level: 0
})

const stickyTop = computed(() => {
  // Position below the parent flow action header
  return `calc(${(props.level ?? 0)} * 2.5rem - 0.1rem)`
})

const isExpanded = ref(props.defaultExpanded)

const filePath = computed(() => {
  return props.fileData.newFile.fileName || props.fileData.oldFile.fileName || 'Unknown file'
})

const viewMode = computed(() => {
  return props.diffMode === 'split' ? DiffModeEnum.Split : DiffModeEnum.Unified
})

const toggleExpanded = () => {
  isExpanded.value = !isExpanded.value
}

const visualSummary = computed(() => {
  const total = props.fileData.linesAdded + props.fileData.linesRemoved + props.fileData.linesUnchanged
  
  if (total === 0) {
    return Array(5).fill({ type: 'unchanged' })
  }

  const addedRatio = props.fileData.linesAdded / total
  const removedRatio = props.fileData.linesRemoved / total

  // Round to nearest 20% (each square represents 20%)
  const addedSquares = Math.round(addedRatio * 5)
  const removedSquares = Math.round(removedRatio * 5)
  const unchangedSquares = 5 - addedSquares - removedSquares

  const squares = []
  
  // Add green squares for additions
  for (let i = 0; i < addedSquares; i++) {
    squares.push({ type: 'added' })
  }
  
  // Add red squares for removals
  for (let i = 0; i < removedSquares; i++) {
    squares.push({ type: 'removed' })
  }
  
  // Add grey squares for unchanged
  for (let i = 0; i < unchangedSquares; i++) {
    squares.push({ type: 'unchanged' })
  }

  return squares
})

const copyFilePath = async () => {
  try {
    await navigator.clipboard.writeText(filePath.value)
  } catch (err) {
    console.error('Failed to copy file path:', err)
  }
}

const getTheme = () => {
  const prefersDarkScheme = window.matchMedia('(prefers-color-scheme: dark)')
  return prefersDarkScheme.matches ? 'dark' : 'light'
}
</script>

<style scoped>
.diff-file {
  border: 1px solid var(--color-border);
  border-radius: 0.375rem;
  background: var(--color-background-soft);
}

.file-header {
  cursor: pointer;
  user-select: none;
  background: var(--color-background-mute);
  border-bottom: 1px solid var(--color-border);
  position: sticky;
  top: v-bind(stickyTop);
  z-index: 10;
}

.file-header:hover {
  background: var(--color-background-hover);
}

.file-header-content {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 0.75rem 1rem;
}

.file-path-container {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  flex: 1;
  min-width: 0;
}

.expand-icon {
  font-size: 0.75rem;
  color: var(--color-text-muted);
  transition: transform 0.2s;
  flex-shrink: 0;
}

.expand-icon.expanded {
  transform: rotate(90deg);
}

.file-path {
  font-family: 'SF Mono', Monaco, 'Cascadia Code', 'Roboto Mono', Consolas, 'Courier New', monospace;
  font-size: 0.875rem;
  color: var(--color-text);
  word-break: break-all;
}

.copy-button {
  background: none;
  border: none;
  cursor: pointer;
  padding: 0.25rem;
  border-radius: 0.25rem;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: background-color 0.2s;
  flex-shrink: 0;
}

.copy-button:hover {
  background: var(--color-background-hover);
}

.copy-button svg {
  width: 1rem;
  height: 1rem;
}

.file-summary {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}

.visual-summary {
  display: flex;
  gap: 0.125rem;
}

.summary-square {
  width: 0.75rem;
  height: 0.75rem;
  border-radius: 0.125rem;
}

.summary-square.added {
  background-color: var(--color-green);
}

.summary-square.removed {
  background-color: #f85149;
}

.summary-square.unchanged {
  background-color: var(--color-border-contrast);
}

.line-counts {
  display: flex;
  gap: 0.5rem;
  font-size: 0.875rem;
  font-weight: 500;
}

.added-count {
  color: var(--color-green);
}

.removed-count {
  color: #f85149;
}

.diff-content {
  padding: 0;
}
</style>