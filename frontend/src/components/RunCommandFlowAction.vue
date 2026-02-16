<template>
  <div class="tool-flow-action">
    <div v-if="expand">
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
  level?: number
}>()

const summary = computed<{ text: string, emoji: string } | null>(() => {
  try {
    const params = props.flowAction.actionParams;
    if (!params?.command) {
      return null;
    }
    
    const command = params.workingDir ? `${params.workingDir}$ ${params.command}` : `${params.command}`;
    
    if (props.flowAction.actionStatus === 'complete') {
      let exitStatus: number | null = null;
      try {
        const parsed = JSON.parse(props.flowAction.actionResult);
        if (parsed && typeof parsed.exitStatus === 'number') {
          exitStatus = parsed.exitStatus;
        }
      } catch {
        // ignore parse errors
      }
      
      const text = exitStatus !== null && exitStatus !== 0 
        ? `${command} (exit ${exitStatus})`
        : command;
      
      return {
        text,
        emoji: exitStatus !== null && exitStatus !== 0 ? '❌' : '',
      };
    }
    
    return { text: command, emoji: '' };
  } catch (error) {
    console.error('Error parsing run_command params:', error);
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
  margin-top: 0.625rem;
}

.action-params {
  margin-top: 0.625rem;
}

code {
  background-color: var(--color-background-mute);
  padding: 0.125rem 0.25rem;
  border-radius: 0.25rem;
}
</style>