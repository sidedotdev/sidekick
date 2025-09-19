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
    if (!params || !params.file_lines || !Array.isArray(params.file_lines) || typeof params.window_size !== 'number') {
      return null;
    }

    const fileGroups: Record<string, string[]> = {};
    
    for (const fileLine of params.file_lines) {
      if (!fileLine.file_path || typeof fileLine.line_number !== 'number') continue;
      
      const a = Math.max(1, fileLine.line_number - params.window_size);
      const b = fileLine.line_number + params.window_size;
      const range = `${a}-${b}`;
      
      if (!fileGroups[fileLine.file_path]) {
        fileGroups[fileLine.file_path] = [];
      }
      fileGroups[fileLine.file_path].push(range);
    }

    const parts = Object.entries(fileGroups).map(([filePath, ranges]) => 
      `${filePath}: ${ranges.join(', ')}`
    );

    return parts.join('; ');
  } catch (error) {
    console.error('Error computing read_file_lines summary:', error);
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