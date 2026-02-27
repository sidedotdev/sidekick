<template>
  <div class="tool-flow-action">
    <div v-if="expand">
      <div class="action-params">
        Params: <JsonTree :data="flowAction.actionParams" :deep="0" />
      </div>
      <div class="action-result">
        <pre v-if="toolResponse">{{ toolResponse }}</pre>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { FlowAction } from '../lib/models';
import JsonTree from './JsonTree.vue'

const props = defineProps<{
  flowAction: FlowAction,
  expand: boolean,
  level?: number
}>()

const toolResponse = computed(() => {
  try {
    const parsed = JSON.parse(props.flowAction.actionResult)
    if (parsed && parsed.Response) {
      return parsed.Response
    }
    return null
  } catch (error) {
    console.error('Error parsing action result:', error)
    return props.flowAction.actionResult
  }
})
</script>

<style scoped>
.tool-flow-action {
  margin-top: 0.625rem;
}

.action-params {
  margin-top: 0.625rem;
}

code {
  background-color: var(--color-background-mute);
  padding: 0.125rem 0.25rem;
  border-radius: 0.25rem;
}
</style>