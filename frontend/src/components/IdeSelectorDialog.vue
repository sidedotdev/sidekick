<template>
  <div v-if="show" class="ide-selector-overlay" @click.self="$emit('cancel')">
    <div class="ide-selector-dialog">
      <h3>Open file in...</h3>
      <div class="ide-selector-buttons">
        <button @click="$emit('select', 'vscode')" class="ide-button">
          <VSCodeIcon />
          <span>VS Code</span>
        </button>
        <button @click="$emit('select', 'intellij')" class="ide-button">
          <IntellijIcon />
          <span>IntelliJ</span>
        </button>
      </div>
      <button @click="$emit('cancel')" class="ide-cancel-button">Cancel</button>
    </div>
  </div>
</template>

<script setup lang="ts">
import VSCodeIcon from '@/components/icons/VSCodeIcon.vue'
import IntellijIcon from '@/components/icons/IntellijIcon.vue'
import type { IdeType } from '@/composables/useIdeOpener'

defineProps<{
  show: boolean
}>()

defineEmits<{
  select: [ide: IdeType]
  cancel: []
}>()
</script>

<style scoped>
.ide-selector-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background-color: rgba(0, 0, 0, 0.5);
  display: flex;
  justify-content: center;
  align-items: center;
  z-index: 2000;
}

.ide-selector-dialog {
  background-color: var(--vp-c-bg);
  border: 1px solid var(--vp-c-divider);
  border-radius: 0.5rem;
  padding: 1.5rem;
  min-width: 16rem;
  text-align: center;
}

.ide-selector-dialog h3 {
  margin: 0 0 1rem 0;
  color: var(--vp-c-text-1);
}

.ide-selector-buttons {
  display: flex;
  gap: 1rem;
  justify-content: center;
  margin-bottom: 1rem;
}

.ide-button {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.5rem;
  padding: 1rem 1.5rem;
  border: 1px solid var(--vp-c-divider);
  border-radius: 0.5rem;
  background-color: var(--vp-c-bg-soft);
  color: var(--vp-c-text-1);
  cursor: pointer;
  transition: background-color 0.2s, border-color 0.2s;
}

.ide-button:hover {
  background-color: var(--vp-c-bg-mute);
  border-color: var(--color-primary);
}

.ide-button > :first-child {
  width: 2rem;
  height: 2rem;
}

.ide-cancel-button {
  padding: 0.5rem 1rem;
  border: 1px solid var(--vp-c-divider);
  border-radius: 0.25rem;
  background-color: transparent;
  color: var(--vp-c-text-2);
  cursor: pointer;
  transition: color 0.2s;
}

.ide-cancel-button:hover {
  color: var(--vp-c-text-1);
}
</style>