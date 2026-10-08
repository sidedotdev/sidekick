<template>
  <div class="local-settings-view">
    <h2>Local Settings</h2>
    <p class="scope-note">Stored in this browser only and shared across all workspaces.</p>

    <section class="setting">
      <label class="toggle">
        <input
          type="checkbox"
          class="keyboard-control-toggle"
          :checked="localSettings.kanbanKeyboardControl"
          @change="onKeyboardControlChange"
        />
        Keyboard control for kanban board
      </label>
      <p class="hint">
        Move focus around the board, sidebar and header with the arrow keys or
        the keys below, and press Enter to open the focused task. Submitting
        the last pending request with
        {{ submitLabel }} returns to the board; {{ submitWithoutNavigationLabel }}
        submits without leaving.
      </p>

      <fieldset class="key-bindings">
        <legend>Secondary movement keys</legend>
        <div v-for="direction in KANBAN_DIRECTIONS" :key="direction" class="key-binding">
          <label :for="`kanban-key-${direction}`">{{ directionLabels[direction] }}</label>
          <input
            :id="`kanban-key-${direction}`"
            type="text"
            class="key-input"
            :data-direction="direction"
            :value="drafts[direction]"
            :aria-invalid="!!errors[direction]"
            autocomplete="off"
            @focus="selectInput"
            @input="(event) => onKeyInput(direction, (event.target as HTMLInputElement).value)"
            @blur="resetDraft(direction)"
          />
          <span v-if="errors[direction]" class="key-error" role="alert">{{ errors[direction] }}</span>
        </div>
      </fieldset>
    </section>
  </div>
</template>

<script setup lang="ts">
import { reactive } from 'vue'
import {
  KANBAN_DIRECTIONS,
  localSettings,
  setKanbanKeyBinding,
  setKanbanKeyboardControl,
  type KanbanDirection,
} from '@/lib/localSettings'

const isMac = typeof navigator !== 'undefined' && navigator.platform.toUpperCase().indexOf('MAC') >= 0
const submitLabel = isMac ? '⌘↵' : 'Ctrl+↵'
const submitWithoutNavigationLabel = isMac ? '⇧⌘↵' : 'Ctrl+Shift+↵'

const directionLabels: Record<KanbanDirection, string> = {
  up: 'Up',
  down: 'Down',
  left: 'Left',
  right: 'Right',
}

const drafts = reactive<Record<KanbanDirection, string>>({ ...localSettings.kanbanKeyBindings })
const errors = reactive<Partial<Record<KanbanDirection, string>>>({})

const onKeyboardControlChange = (event: Event) => {
  setKanbanKeyboardControl((event.target as HTMLInputElement).checked)
}

const onKeyInput = (direction: KanbanDirection, value: string) => {
  drafts[direction] = value
  const error = setKanbanKeyBinding(direction, value)
  if (error) {
    errors[direction] = error
  } else {
    delete errors[direction]
  }
}

// Invalid drafts never replace the saved binding, so leaving the field shows it again
const resetDraft = (direction: KanbanDirection) => {
  drafts[direction] = localSettings.kanbanKeyBindings[direction]
  delete errors[direction]
}

const selectInput = (event: FocusEvent) => {
  (event.target as HTMLInputElement).select()
}
</script>

<style scoped>
.local-settings-view {
  max-width: 40rem;
  padding: 2rem;
}

.local-settings-view h2 {
  margin: 0 0 0.5rem 0;
}

.scope-note,
.hint {
  color: var(--color-text-2);
  font-size: 0.9rem;
}

.scope-note {
  margin: 0 0 1.5rem 0;
}

.toggle {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-weight: 500;
  cursor: pointer;
}

.hint {
  margin: 0.5rem 0 1rem 0;
}

.key-bindings {
  border: 1px solid var(--color-border-contrast);
  border-radius: 0.25rem;
  padding: 0.75rem 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.key-bindings legend {
  padding: 0 0.25rem;
  font-size: 0.9rem;
}

.key-binding {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}

.key-binding label {
  width: 3.5rem;
}

.key-input {
  width: 3rem;
  padding: 0.25rem 0.5rem;
  text-align: center;
  font-family: "JetBrains Mono", monospace;
  background-color: var(--color-background);
  color: var(--color-text);
  border: 1px solid var(--color-border-contrast);
  border-radius: 0.25rem;
}

.key-input[aria-invalid="true"] {
  border-color: var(--color-error-border);
}

.key-error {
  color: var(--color-error-text);
  font-size: 0.85rem;
}
</style>