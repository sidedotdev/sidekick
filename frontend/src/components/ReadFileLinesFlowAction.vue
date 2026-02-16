<template>
  <div v-if="expand" class="tool-flow-action">
    <div class="action-params">
      Params: <JsonTree :data="flowAction.actionParams" :deep="0" />
    </div>
    <div class="action-result">
      <template v-if="contentBlocks && contentBlocks.length > 0">
        <template v-for="(block, idx) in contentBlocks" :key="idx">
          <pre v-if="block.type === 'text' && block.text" class="tool-result-text">{{ block.text }}</pre>
          <img v-else-if="block.type === 'image' && block.image?.url" :src="block.image.url" class="tool-result-image" />
          <JsonTree v-else :deep="0" :data="block" />
        </template>
      </template>
      <pre v-else-if="toolResponse">{{ toolResponse }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { FlowAction, Llm2ContentBlock } from '../lib/models';
import JsonTree from './JsonTree.vue'

const props = defineProps<{
  flowAction: FlowAction,
  expand: boolean,
  level?: number
}>()

const summary = computed<{ text: string, emoji: string } | null>(() => {
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

    return { text: parts.join('; '), emoji: '📄' };
  } catch (error) {
    console.error('Error computing read_file_lines summary:', error);
    return null;
  }
});

defineExpose({ summary });

const parsedResult = computed(() => {
  try {
    return JSON.parse(props.flowAction.actionResult)
  } catch {
    return null
  }
})

const contentBlocks = computed<Llm2ContentBlock[] | null>(() => {
  const parsed = parsedResult.value
  if (parsed?.content && Array.isArray(parsed.content)) {
    return parsed.content
  }
  const trc = parsed?.toolResultContent ?? parsed?.ToolResultContent
  if (trc && Array.isArray(trc)) {
    return trc
  }
  return null
})

const toolResponse = computed<string | null>(() => {
  const parsed = parsedResult.value
  return parsed?.response ?? parsed?.Response ?? null
})
</script>

<style scoped>
.tool-flow-action {
  margin-top: 0.625rem;
}

.action-params {
  margin-top: 0.625rem;
}
</style>