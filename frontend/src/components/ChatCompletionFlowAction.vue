<template>
  <div v-if="expand">

    <div class="model-summary">
      <p class="model-provider" v-if="effectiveProvider">Provider: {{ effectiveProvider }}</p>
      <p class="model-name" v-if="effectiveModel">
        Model: {{ effectiveModel }}
        <span class="model-reasoning-effort" v-if="flowAction.actionParams.reasoningEffort">
          ({{ flowAction.actionParams.reasoningEffort }} reasoning)
        </span>
      </p>
      <p class="model-usage" v-if="parsedActionResult && !completionParseFailure && flowAction.actionStatus !== 'pending' && flowAction.actionStatus !== 'started' && usage && (usage.inputTokens || usage.outputTokens)">
        <span v-if="usage.inputTokens">{{ formatTokens(usage.inputTokens) }} in</span><span v-if="usage.inputTokens && (usage.cacheReadInputTokens || usage.cacheWriteInputTokens)"> (<span v-if="usage.cacheReadInputTokens">{{ formatTokens(usage.cacheReadInputTokens) }} cached</span><span v-if="usage.cacheReadInputTokens && usage.cacheWriteInputTokens">, </span><span v-if="usage.cacheWriteInputTokens">{{ formatTokens(usage.cacheWriteInputTokens) }} written</span>)</span><span v-if="usage.inputTokens && usage.outputTokens"> · </span><span v-if="usage.outputTokens">{{ formatTokens(usage.outputTokens) }} out</span>
      </p>
    </div>

    Message History: <a @click="showParams = !showParams" class="show-params">{{ showParams ? 'Hide' : 'Show' }}</a>
    <div class="action-params" v-if="showParams">
      <p class="model-reasoning-effort" v-if="flowAction.actionParams.reasoningEffort">
        Requested Reasoning Effort: {{ flowAction.actionParams.reasoningEffort }}
      </p>
      <div v-for="(message, index) in messages" :key="index" class="message">
        <p class="message-role"><span v-text="message.role"></span>:</p>

        <div v-if="message.content"
          :class="{
            'expanded': expandedMessages.includes(index),
            'truncated': !expandedMessages.includes(index)
          }"
          class="message-content historical"
        >
          <vue-markdown v-if="message.role == 'assistant'"
            :source="message.content"
            :options="{ breaks: true }"
            class="markdown"
          />
          <pre v-else v-text="message.content"></pre>
        </div>

        <div v-if="message.function_call" class="message-function-call" :class="{ 'expanded': expandedMessages.includes(index), 'truncated': !expandedMessages.includes(index) }">
          Function Call: <span v-text="message.function_call?.name" class="message-function-call-name"></span>
          <JsonTree :deep="1" :data="JSON.parse(message.function_call?.arguments || '{}')" />
        </div>
        <div v-for="toolCall in message.toolCalls" :key="toolCall.id" class="message-function-call" :class="{ 'expanded': expandedMessages.includes(index), 'truncated': !expandedMessages.includes(index) }">
          Tool Call: <span v-text="toolCall.name" class="message-function-call-name"></span>
          <JsonTree :deep="1" :data="toolCall.parsedArguments" />
        </div>
        <button @click="toggleMessage(index)">
          {{ expandedMessages.includes(index) ? "Show Less" : "Show More" }}
        </button>
      </div>
    </div>

    <div class="action-result" v-if="flowAction.actionResult != '' || flowAction.streamingData || (flowAction.actionStatus != 'pending' && flowAction.actionStatus != 'started')">

      <!-- Streaming state: render partial content -->
      <template v-if="isStreaming && streamingData">
        <vue-markdown v-if="streamingData.content" :options="{ breaks: true }" :source="streamingData.content" class="message-content markdown"/>
        <div v-for="toolCall in streamingData.toolCalls" :key="toolCall.id" class="streaming-tool-call">
          <p v-if="toolCall.name" class="action-result-function-name">Tool Call: {{ toolCall.name }}</p>
          <vue-markdown :options="{ breaks: true }" :source="'```tool_call\n' + (toolCall.arguments || '') + '\n```'" class="tool-call-arguments"/>
        </div>
      </template>

      <!-- Completed state: render from parsed actionResult -->
      <template v-else>
        <div v-if="completionParseFailure" class="error-message">
          <div v-if="flowAction.actionStatus != 'pending' && flowAction.actionStatus != 'started'">
            Error: {{ completionParseFailure }}
          </div>
          <pre>{{ flowAction.actionResult }}</pre>
        </div>
        <vue-markdown v-if="completion?.content" :options="{ breaks: true }" :source="completion?.content" class="message-content markdown"/>
        <div v-for="toolCall in completion?.toolCalls" :key="toolCall.id">
          <p class="action-result-function-name">Tool Call: {{ toolCall.name }}</p>
          <JsonTree :deep="1" :data="toolCall.parsedArguments || JSON.parse(toolCall.arguments || '{}')" class="action-result-function-args"/>
        </div>
        <div v-if="parsedActionResult && !completion?.toolCalls?.length && !completion?.content">
          <JsonTree :deep="1" :data="parsedActionResult" class="action-result-parsed"/>
        </div>
        <p v-if="completion?.stopReason" class="action-result-stop-reason">Stop Reason: {{ completion?.stopReason }}</p>
      </template>
    </div>
  </div>
  <div v-if="debug" class="action-debug">
    <p>Params: <JsonTree :data="flowAction.actionParams"/></p>
    <p>Result: <JsonTree :data="JSON.parse(flowAction.actionResult || '{}')"/></p>
  </div>
</template>

<script setup lang="ts">
import type { ChatCompletionChoice, ChatCompletionMessage, FlowAction, Usage, StreamingData } from '../lib/models';
import { computed, ref, watch } from 'vue'
import JsonTree from './JsonTree.vue'
import VueMarkdown from 'vue-markdown-render'

const props = defineProps({
  flowAction: {
    type: Object as () => FlowAction,
    required: true,
  },
  expand: {
    type: Boolean,
    required: true,
  }
})

const showParams = ref(false);
const debug = ref(false);
const expandedMessages = ref<number[]>([])
const messages = computed(() => {
  const msgs = props.flowAction.actionParams.messages;
  if (msgs) {
    msgs.forEach(addParsedArguments);
  }
  return msgs || [];
});

const completionParseFailure = ref<string | null>(null);

const parsedActionResult = ref((() => {
  let result: any | null = null;
  try {
    if (props.flowAction.actionResult) {
      result = JSON.parse(props.flowAction.actionResult);
    }
  } catch (e: any) {
    completionParseFailure.value = e.message;
  }
  return result;
})());

const completion = computed<ChatCompletionChoice>(() => parsedActionResult.value || {});

const effectiveModel = computed(() => props.flowAction.actionParams.model || completion.value?.model || '')
const effectiveProvider = computed(() => props.flowAction.actionParams.provider || completion.value?.provider || '')

const usage = computed<Usage | null>(() => completion.value?.usage || null)

const isStreaming = computed(() => props.flowAction.actionStatus === 'started')
const streamingData = computed<StreamingData | null>(() => props.flowAction.streamingData || null)

function formatTokens(n: number): string {
  if (n >= 1000) {
    const formatted = (n / 1000).toFixed(1)
    return formatted.endsWith('.0') ? formatted.slice(0, -2) + 'k' : formatted + 'k'
  }
  return n.toString()
}

// Watcher for flowAction changes
watch(() => props.flowAction, (newVal) => {
  // Skip JSON parsing during streaming - we use streamingData instead
  if (newVal.actionStatus === 'started') {
    return;
  }

  try {
    if (newVal.actionResult) {
      parsedActionResult.value = JSON.parse(newVal.actionResult);
      completionParseFailure.value = null;
    }
  } catch (e: any) {
    if (!(e instanceof Error)) { throw e; }
    if (/JSON/.test(e.message)) {
      completionParseFailure.value = "Invalid JSON string in actionResult";
    } else {
      completionParseFailure.value = e.message;
    }
  }

  if (completion.value?.toolCalls?.length) {
    try {
      addParsedArguments(completion.value);
    } catch (e: any) {
      if (!(e instanceof Error)) { throw e; }
      if (/JSON/.test(e.message)) {
        completionParseFailure.value = "Invalid JSON string in tool call arguments";
      } else {
        completionParseFailure.value = e.message;
      }
    }
  }
}, { immediate: true, deep: true });

function addParsedArguments(message: ChatCompletionMessage) {
  message.toolCalls?.forEach((toolCall) => {
    if (toolCall.arguments) {
      try {
        toolCall.parsedArguments = JSON.parse(toolCall.arguments as string)
      } catch (e: any) {
        if (!(e instanceof Error)) { throw e; }
        if (/JSON/.test(e.message)) {
            toolCall.parsedArguments = `Error: Invalid JSON string in tool call arguments: ${ toolCall.arguments }`
        } else {
          throw e
        }
      }
    }
  })
}

function toggleMessage(index: number) {
  if (expandedMessages.value.includes(index)) {
    const i = expandedMessages.value.indexOf(index)
    expandedMessages.value.splice(i, 1)
  } else {
    expandedMessages.value.push(index)
  }
  return false
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

.message-role, .message-role * {
  font-weight: bold;
}
.message-content.historical {
  max-height: 100px;
  padding-left: 10px;
  overflow: hidden;
}

.message-content.historical.expanded {
  max-height: none;
}

.truncated {
  max-height: 100px;
  overflow: hidden;
  text-overflow: ellipsis;
}

.action-result-stop-reason {
  font-size: 12px;
}

.message-function-call-name {
  color: #f92;
}

.model-usage {
  font-size: 0.85em;
  color: var(--color-text-2);
  font-weight: normal;
}

.streaming-tool-call {
  margin-bottom: 1rem;
}

.tool-call-arguments :deep(pre) {
  border: 2px solid var(--color-border-contrast);
  padding: 1rem;
  margin: 0;
  overflow-x: auto;
  white-space: pre-wrap;
  word-break: break-word;
}

.tool-call-arguments :deep(code) {
  font-family: monospace;
}

</style>