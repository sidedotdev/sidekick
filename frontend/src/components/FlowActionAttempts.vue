<template>
  <details v-if="attempts.length > 0" class="flow-action-attempts" :open="open" @toggle="onToggle">
    <summary>{{ summaryText }}</summary>
    <ol>
      <li v-for="(attempt, index) in attempts" :key="index">
        <time :datetime="attempt.at">{{ formatAttemptTime(attempt.at) }}</time>
        <pre>{{ attempt.error }}</pre>
      </li>
    </ol>
  </details>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { FlowActionAttempt } from '../lib/models'
import { failedAttemptsSummary } from '../lib/retryAttempts'

const props = defineProps<{
  attempts: FlowActionAttempt[]
  open?: boolean
}>()

const emit = defineEmits<{
  (e: 'update:open', value: boolean): void
}>()

const summaryText = computed(() => failedAttemptsSummary({ attempts: props.attempts }))

const onToggle = (event: Event) => {
  emit('update:open', (event.target as HTMLDetailsElement).open)
}

const formatAttemptTime = (at: string): string => new Date(at).toLocaleString()
</script>

<style scoped>
.flow-action-attempts {
  margin: 0.5rem 0;
}

summary {
  cursor: pointer;
  color: var(--color-text-2);
}

ol {
  margin: 0.5rem 0 0;
  padding-left: 1.5rem;
}

li {
  margin-bottom: 0.5rem;
}

time {
  font-size: 0.85em;
  color: var(--color-text-2);
}

pre {
  margin: 0.25rem 0 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-size: 0.85em;
}
</style>