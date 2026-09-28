<template>
  <template v-for="(block, blockIndex) in blocks" :key="blockIndex">
    <div v-if="block.type === 'text' && block.text" class="llm2-text-block">
      <vue-markdown :options="{ breaks: true }" :source="block.text" class="message-content markdown"/>
    </div>
    <div v-else-if="block.type === 'image' && block.image?.url" class="llm2-image-block">
      <ImagePreview :src="block.image.url" />
    </div>
    <div v-else-if="block.type === 'tool_use' && block.toolUse" class="llm2-tool-use-block">
      <p class="action-result-function-name">Tool Call: {{ block.toolUse.name }}</p>
      <JsonTree :deep="jsonTreeDepth" :data="argumentsFor(block, blockIndex)" class="action-result-function-args"/>
    </div>
    <div v-else-if="block.type === 'reasoning'" class="llm2-text-block">
      <vue-markdown v-if="block.reasoning?.text" :options="{ breaks: true }" :source="block.reasoning.text" class="message-content markdown reasoning"/>
      <p v-else class="reasoning-redacted"><em>Reasoning (content not available)</em></p>
      <p v-if="block.reasoning?.summary" class="reasoning-summary"><strong>Summary:</strong> {{ block.reasoning.summary }}</p>
    </div>
    <BuiltinToolBlock v-else-if="block.type === 'builtin_tool_use'" :block="block" :json-tree-depth="jsonTreeDepth" />
    <div v-else-if="block.type !== 'text'" class="llm2-unknown-block">
      <JsonTree :deep="jsonTreeDepth" :data="block"/>
    </div>
  </template>
</template>

<script setup lang="ts">
import VueMarkdown from 'vue-markdown-render'
import type { Llm2ContentBlock, Llm2ToolUseBlock } from '../lib/models'
import { parseLlm2ToolArguments } from '../lib/toolArguments'
import JsonTree from './JsonTree.vue'
import ImagePreview from './ImagePreview.vue'
import BuiltinToolBlock from './BuiltinToolBlock.vue'

const props = withDefaults(defineProps<{
  blocks: Llm2ContentBlock[]
  jsonTreeDepth?: number
  /**
   * Already-parsed tool_use arguments keyed by block index, for callers that
   * parse arguments incrementally. Blocks without an entry are parsed from
   * their arguments string.
   */
  toolArguments?: Record<number, object>
}>(), {
  jsonTreeDepth: 1,
  toolArguments: () => ({})
})

function argumentsFor(block: Llm2ToolUseBlock, index: number): object {
  return props.toolArguments[index] ?? parseLlm2ToolArguments(block.toolUse.arguments)
}
</script>

<style scoped>
.message-content :deep(p), .message-content :deep(ul), .message-content :deep(ol) {
  margin-bottom: 0.5rem;
}
.message-content :deep(ul), .message-content :deep(ol) {
  margin-top: 1rem;
  margin-bottom: 1rem;
}
.message-content :deep(li) {
  margin-bottom: 0.25rem;
}

.markdown :deep(pre) {
  border: 2px solid var(--color-border-contrast);
  padding: 1rem;
  margin-bottom: 1rem;
}

.llm2-text-block {
  margin-bottom: 0.5rem;
}

.llm2-tool-use-block {
  margin-bottom: 0.5rem;
}

.llm2-image-block {
  margin-bottom: 0.5rem;
}

.llm2-unknown-block {
  margin-bottom: 0.5rem;
  padding: 0.5rem;
  background: var(--color-background-soft);
  border-radius: 4px;
}
</style>