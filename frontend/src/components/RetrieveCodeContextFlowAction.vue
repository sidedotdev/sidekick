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
    if (!params) {
      return null;
    }

    const requests = params.requests || params.code_context_requests;
    if (!requests || !Array.isArray(requests)) {
      return null;
    }

    const fileGroups: Record<string, string[]> = {};
    
    for (const request of requests) {
      if (!request.file_path) continue;
      
      const symbols = request.symbol_names && Array.isArray(request.symbol_names) && request.symbol_names.length > 0
        ? request.symbol_names
        : ['(full)'];
      
      fileGroups[request.file_path] = symbols;
    }

    const parts = Object.entries(fileGroups).map(([filePath, symbols]) => 
      `${filePath}: ${symbols.join(', ')}`
    );

    return { text: parts.join('; '), emoji: '📖' };
  } catch (error) {
    console.error('Error computing get_symbol_definitions summary:', error);
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