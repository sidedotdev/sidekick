<template>
  <div v-if="expand" class="tool-flow-action">
    <div class="action-params">
      Params: <JsonTree :data="flowAction.actionParams" :deep="0" />
    </div>
    <div class="action-result">
      <template v-if="contentBlocks && contentBlocks.length > 0">
        <template v-for="(block, idx) in contentBlocks" :key="idx">
          <pre v-if="block.type === 'text' && block.text" class="tool-result-text">{{ block.text }}</pre>
          <ImagePreview v-else-if="block.type === 'image' && block.image?.url" :src="block.image.url" />
          <div v-else-if="block.type === 'tool_result' && block.toolResult?.content?.length" class="nested-tool-result">
            <template v-for="(nested, nIdx) in block.toolResult.content" :key="nIdx">
              <pre v-if="nested.type === 'text' && nested.text" class="tool-result-text">{{ nested.text }}</pre>
              <ImagePreview v-else-if="nested.type === 'image' && nested.image?.url" :src="nested.image.url" />
              <JsonTree v-else :deep="0" :data="nested" />
            </template>
          </div>
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
import ImagePreview from './ImagePreview.vue'

const props = defineProps<{
  flowAction: FlowAction,
  expand: boolean,
}>()

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
  margin-top: 10px;
}

.action-params {
  margin-top: 10px;
}

</style>