<template>
  <div v-if="expand" class="plaintext-container">
    <div class="action-params">
      Params: <JsonTree :data="flowAction.actionParams" :deep="0" style="display: inline-block; vertical-align: text-bottom;" />
    </div>
    <div class="action-result">
      <pre>{{ actionResult }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { FlowAction } from '../lib/models';
import JsonTree from './JsonTree.vue'

const props = defineProps({
  flowAction: {
    type: Object as () => FlowAction,
    required: true,
  },
  expand: {
    type: Boolean,
    required: true,
  }
})

const actionResult = computed(() => {
  try {
    const parsed = JSON.parse(props.flowAction.actionResult)
    if (typeof parsed === 'string') {
      return parsed
    } else {
      return props.flowAction.actionResult
    }
  } catch (e) {
    return props.flowAction.actionResult
  }
})
</script>

<style scoped>
.action-params, .action-result {
  margin-bottom: 1rem;
}
pre {
  overflow-x: scroll;
  white-space: pre-wrap;
}

h3 {
  font-size: 1.2rem;
  margin-bottom: 0.5rem;
}
</style>