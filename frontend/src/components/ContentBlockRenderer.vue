<template>
  <pre v-if="block.type === 'text' && block.text" class="tool-result-text">{{ block.text }}</pre>
  <ImagePreview v-else-if="block.type === 'image' && block.image?.url" :src="block.image.url" />
  <div v-else-if="block.type === 'tool_result' && block.toolResult?.content?.length" class="nested-tool-result">
    <template v-for="(nested, nIdx) in block.toolResult.content" :key="nested.id ?? nIdx">
      <ContentBlockRenderer :block="nested" />
    </template>
  </div>
  <JsonTree v-else :deep="0" :data="block" />
</template>

<script setup lang="ts">
import type { Llm2ContentBlock } from '../lib/models'
import ImagePreview from './ImagePreview.vue'
import JsonTree from './JsonTree.vue'

defineProps<{
  block: Llm2ContentBlock
}>()
</script>