<template>
  <div v-if="expand" class="tool-flow-action">
    <div class="action-params">
      Params: <JsonTree :data="flowAction.actionParams" :deep="0" />
    </div>
    <div class="action-result">
      <div v-if="hydrationLoading" class="hydration-loading">Loading content...</div>
      <template v-if="hydratedBlocks && hydratedBlocks.length > 0">
        <template v-for="(block, idx) in hydratedBlocks" :key="'h-' + idx">
          <ContentBlockRenderer :block="block" />
        </template>
      </template>
      <template v-else-if="contentBlocks && contentBlocks.length > 0">
        <template v-for="(block, idx) in contentBlocks" :key="idx">
          <ContentBlockRenderer :block="block" />
        </template>
      </template>
      <pre v-else-if="toolResponse">{{ toolResponse }}</pre>
      <p v-else-if="flowAction.actionStatus === 'started'" class="running-indicator">Running…</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { FlowAction, Llm2ContentBlock } from '../lib/models';
import ContentBlockRenderer from './ContentBlockRenderer.vue'
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
  if (parsed) {
    return parsed.response ?? parsed.Response ?? null
  }
  return props.flowAction.actionResult || null
})

const hydrationLoading = ref(false)
const hydratedBlocks = ref<Llm2ContentBlock[] | null>(null)

const resultRef = computed(() => {
  const parsed = parsedResult.value
  if (parsed?.ref?.blockKeys?.length) {
    return parsed.ref
  }
  return null
})

watch(
  () => [props.expand, resultRef.value] as const,
  async ([expanded, refVal]) => {
    if (!expanded || !refVal) {
      hydratedBlocks.value = null
      return
    }

    hydrationLoading.value = true
    try {
      const response = await fetch(
        `/api/v1/workspaces/${props.flowAction.workspaceId}/flows/${props.flowAction.flowId}/chat_history/hydrate`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ refs: [refVal] }),
        }
      )
      if (!response.ok) {
        hydratedBlocks.value = null
        return
      }
      const data = await response.json()
      const messages = data.messages || []
      const blocks: Llm2ContentBlock[] = []
      for (const msg of messages) {
        if (msg.content) {
          blocks.push(...msg.content)
        }
      }
      hydratedBlocks.value = blocks.length > 0 ? blocks : null
    } catch {
      hydratedBlocks.value = null
    } finally {
      hydrationLoading.value = false
    }
  },
  { immediate: true }
)
</script>

<style scoped>
.tool-flow-action {
  margin-top: 0.625rem;
}

.action-params {
  margin-top: 0.625rem;
}

.running-indicator {
  color: var(--color-text-2);
  font-style: italic;
  margin: 0.5rem 0;
}
</style>