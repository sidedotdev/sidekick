<template>
  <div v-if="expand" class="tool-flow-action">
    <div v-if="summary" class="action-summary-section">
      <strong>Summary:</strong> {{ summary }}
    </div>
    <div class="action-params">
      Params: <JsonTree :data="flowAction.actionParams" :deep="0" />
    </div>
    <div class="action-result">
      <pre v-if="toolResponse">{{ toolResponse }}</pre>
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

const summary = computed(() => {
  try {
    const params = props.flowAction.actionParams;
    if (!params || !params.code_context_requests || !Array.isArray(params.code_context_requests)) {
      return null;
    }

    const fileGroups: Record<string, string[]> = {};
    
    for (const request of params.code_context_requests) {
      if (!request.file_path) continue;
      
      const symbols = request.symbol_names && Array.isArray(request.symbol_names) && request.symbol_names.length > 0
        ? request.symbol_names
        : ['(full)'];
      
      fileGroups[request.file_path] = symbols;
    }

    const parts = Object.entries(fileGroups).map(([filePath, symbols]) => 
      `${filePath}: ${symbols.join(', ')}`
    );

    return parts.join('; ');
  } catch (error) {
    console.error('Error computing retrieve_code_context summary:', error);
    return null;
  }
});

const toolResponse = computed(() => {
  try {
    const parsed = JSON.parse(props.flowAction.actionResult)
    if (parsed && parsed.Response) {
      return parsed.Response
    }
    return null
  } catch (error) {
    console.error('Error parsing action result:', error)
    return null
  }
})
</script>

<style scoped>
.tool-flow-action {
  margin-top: 0.625rem;
}

.action-summary-section {
  margin-top: 0.625rem;
}

.action-params {
  margin-top: 0.625rem;
}
</style>