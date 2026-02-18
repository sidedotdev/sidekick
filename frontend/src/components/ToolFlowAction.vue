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
      <template v-if="contentBlocks && contentBlocks.length > 0">
        <template v-for="(block, idx) in contentBlocks" :key="idx">
          <ContentBlockRenderer :block="block" />
        </template>
      </template>
      <pre v-else-if="!hydratedBlocks?.length && toolResponse">{{ toolResponse }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { FlowAction, Llm2ContentBlock } from '../lib/models';
import JsonTree from './JsonTree.vue'
import ContentBlockRenderer from './ContentBlockRenderer.vue'

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
  margin-top: 10px;
}

.action-params {
  margin-top: 10px;
}

</style>