<template>
  <div v-if="expand" class="tool-flow-action">
    <div class="action-params">
      Params: <JsonTree :data="flowAction.actionParams" :deep="0" />
    </div>
    <div class="action-result">
      <template v-if="contentBlocks && contentBlocks.length > 0">
        <template v-for="(block, idx) in contentBlocks" :key="idx">
          <ContentBlockRenderer :block="block" />
        </template>
      </template>
      <pre v-else-if="toolResponse">{{ toolResponse }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { FlowAction, Llm2ContentBlock } from '../lib/models';
import ContentBlockRenderer from './ContentBlockRenderer.vue'

const props = defineProps<{
  flowAction: FlowAction,
  expand: boolean,
  level?: number
}>()

const summary = computed<{ text: string, emoji: string } | null>(() => {
  try {
    const params = props.flowAction.actionParams;
    if (!params || !params.searches || !Array.isArray(params.searches)) {
      return null;
    }

    const globGroups: Record<string, string[]> = {};
    
    for (const search of params.searches) {
      if (!search.path_glob || !search.search_term) continue;
      
      if (!globGroups[search.path_glob]) {
        globGroups[search.path_glob] = [];
      }
      globGroups[search.path_glob].push(search.search_term);
    }

    const parts = Object.entries(globGroups).map(([pathGlob, searchTerms]) => 
      `${pathGlob}: ${searchTerms.join(', ')}`
    );

    return { text: parts.join('; '), emoji: '🔎' };
  } catch (error) {
    console.error('Error computing bulk_search_repository summary:', error);
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