<template>
  <div class="tool-flow-action">
    <div v-if="expand">
      <div v-if="commandDisplay" class="command-block">
        <pre class="command-line"><code>{{ commandDisplay }}</code></pre>
      </div>
      <div v-else class="action-params">
        <JsonTree :data="flowAction.actionParams" :deep="0" />
      </div>
      <div class="action-result">
        <template v-if="contentBlocks && contentBlocks.length > 0">
          <template v-for="(block, idx) in contentBlocks" :key="idx">
            <ContentBlockRenderer :block="block" />
          </template>
        </template>
        <pre v-else-if="toolResponse">{{ toolResponse }}</pre>
        <p v-else-if="flowAction.actionStatus === 'started'" class="running-indicator">Running…</p>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { FlowAction, Llm2ContentBlock } from '../lib/models';
import JsonTree from './JsonTree.vue'
import ContentBlockRenderer from './ContentBlockRenderer.vue'

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

const commandDisplay = computed(() => {
  const params = props.flowAction.actionParams;
  if (!params?.command) return null;
  const prefix = params.workingDir ? `${params.workingDir}$ ` : '$ ';
  return prefix + params.command;
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
</script>

<style scoped>
.tool-flow-action {
  margin-top: 0.625rem;
}

.action-params {
  margin-top: 0.625rem;
}

.command-block {
  margin-top: 0.5rem;
}

.command-line {
  background-color: var(--color-background-mute);
  padding: 0.5rem 0.75rem;
  border-radius: 0.25rem;
  overflow-x: auto;
  margin: 0;
}

.command-line code {
  white-space: pre-wrap;
  word-break: break-all;
}

.running-indicator {
  color: var(--color-text-2);
  font-style: italic;
  margin: 0.5rem 0;
}
</style>