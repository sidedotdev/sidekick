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
import type { FlowAction } from '../lib/models';
import type { Llm2ToolResultContentBlock } from '../lib/models';
import JsonTree from './JsonTree.vue'

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

const contentBlocks = computed<Llm2ToolResultContentBlock[] | null>(() => {
  const parsed = parsedResult.value
  if (parsed?.content && Array.isArray(parsed.content)) {
    return parsed.content
  }
  if (parsed?.toolResultContent && Array.isArray(parsed.toolResultContent)) {
    return parsed.toolResultContent
  }
  return null
})

const toolResponse = computed<string | null>(() => {
  const parsed = parsedResult.value
  if (parsed?.Response) {
    return parsed.Response
  }
  return null
})
</script>

<style scoped>
.tool-flow-action {
  margin-top: 10px;
}

.action-params {
  margin-top: 10px;
}

.tool-result-image {
  max-width: 100%;
  margin: 0.5em 0;
}
</style>