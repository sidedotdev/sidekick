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

      <!-- llm2 hydration states -->
      <div v-if="isLlm2Format && llm2HydrationLoading" class="llm2-loading">
        Loading message history...
      </div>
      <div v-else-if="isLlm2Format && llm2HydrationError" class="llm2-error">
        Error loading message history: {{ llm2HydrationError }}
      </div>

      <!-- llm2 format messages -->
      <template v-else-if="isLlm2Format">
        <div v-for="(message, index) in hydratedLlm2Messages" :key="index" class="message">
          <p class="message-role"><span v-text="message.role"></span>:</p>
          <div
            :class="{
              'expanded': expandedMessages.includes(index),
              'truncated': !expandedMessages.includes(index)
            }"
            class="message-content historical llm2-blocks"
          >
            <template v-for="(block, blockIndex) in message.content" :key="blockIndex">
              <!-- text block -->
              <div v-if="block.type === 'text'" class="llm2-text-block">
                <vue-markdown v-if="message.role === 'assistant'"
                  :source="getTextBlockText(block)"
                  :options="{ breaks: true }"
                  class="markdown"
                />
                <pre v-else v-text="getTextBlockText(block)"></pre>
              </div>

              <!-- tool_use block -->
              <div v-else-if="block.type === 'tool_use' && getToolUseBlock(block)" class="llm2-tool-use-block message-function-call">
                Tool Call: <span class="message-function-call-name">{{ getToolUseBlock(block)!.name }}</span>
                <JsonTree :deep="1" :data="parseLlm2ToolArguments(getToolUseBlock(block)!.arguments)" />
              </div>

              <!-- tool_result block -->
              <div v-else-if="block.type === 'tool_result' && getToolResultBlock(block)" class="llm2-tool-result-block">
                <p class="tool-result-header">
                  Tool Result<span v-if="getToolResultBlock(block)!.name">: {{ getToolResultBlock(block)!.name }}</span>
                  <span v-if="getToolResultBlock(block)!.isError" class="tool-result-error"> (error)</span>
                </p>
                <pre v-if="getToolResultBlock(block)!.text" class="tool-result-text">{{ getToolResultBlock(block)!.text }}</pre>
                <JsonTree v-else :deep="1" :data="getToolResultBlock(block)" />
              </div>

              <!-- unknown block fallback -->
              <div v-else class="llm2-unknown-block">
                <p class="unknown-block-header">Unknown Block ({{ block.type }}):</p>
                <JsonTree :deep="1" :data="block" />
              </div>
            </template>
          </div>
          <button @click="toggleMessage(index)">
            {{ expandedMessages.includes(index) ? "Show Less" : "Show More" }}
          </button>
        </div>
      </template>

      <!-- Legacy format messages -->
      <template v-else>
        <div v-for="(message, index) in legacyMessages" :key="index" class="message">
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
      </template>
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

        <!-- llm2 MessageResponse format -->
        <template v-if="isLlm2Response">
          <template v-for="(block, blockIndex) in llm2ResponseBlocks" :key="blockIndex">
            <div v-if="block.type === 'text' && block.text" class="llm2-text-block">
              <vue-markdown :options="{ breaks: true }" :source="block.text" class="message-content markdown"/>
            </div>
            <div v-else-if="block.type === 'tool_use' && block.toolUse" class="llm2-tool-use-block">
              <p class="action-result-function-name">Tool Call: {{ block.toolUse.name }}</p>
              <JsonTree :deep="1" :data="parseLlm2ToolArguments(block.toolUse.arguments)" class="action-result-function-args"/>
            </div>
            <div v-else-if="block.type === 'reasoning'" class="llm2-text-block">
              <vue-markdown v-if="block.reasoning && block.reasoning.text" :options="{ breaks: true }" :source="block.reasoning.text" class="message-content markdown reasoning"/>
            </div>
            <div v-else-if="block.type !== 'text'" class="llm2-unknown-block">
              <JsonTree :deep="1" :data="block"/>
            </div>
          </template>
          <div v-if="parsedActionResult && !llm2ResponseBlocks.length && !completionParseFailure">
            <JsonTree :deep="1" :data="parsedActionResult" class="action-result-parsed"/>
          </div>
        </template>

        <!-- Legacy ChatCompletionChoice format -->
        <template v-else>
          <vue-markdown v-if="completion?.content" :options="{ breaks: true }" :source="completion?.content" class="message-content markdown"/>
          <div v-for="toolCall in completion?.toolCalls" :key="toolCall.id">
            <p class="action-result-function-name">Tool Call: {{ toolCall.name }}</p>
            <JsonTree :deep="1" :data="toolCall.parsedArguments || JSON.parse(toolCall.arguments || '{}')" class="action-result-function-args"/>
          </div>
          <div v-if="parsedActionResult && !completion?.toolCalls?.length && !completion?.content">
            <JsonTree :deep="1" :data="parsedActionResult" class="action-result-parsed"/>
          </div>
        </template>

        <p v-if="completion?.stopReason || parsedActionResult?.stopReason" class="action-result-stop-reason">Stop Reason: {{ completion?.stopReason || parsedActionResult?.stopReason }}</p>
      </template>
    </div>
  </div>
  <div v-if="debug" class="action-debug">
    <p>Params: <JsonTree :data="flowAction.actionParams"/></p>
    <p>Result: <JsonTree :data="JSON.parse(flowAction.actionResult || '{}')"/></p>
  </div>
</template>

<script setup lang="ts">
import type { ChatCompletionChoice, ChatCompletionMessage, FlowAction, Usage, StreamingData, Llm2Message, Llm2ContentBlock, ChatHistoryParamPayload } from '../lib/models';
import { isLlm2ChatHistoryWrapper } from '../lib/models';
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

// llm2 hydration state
const llm2Messages = ref<Llm2Message[] | null>(null)
const llm2HydrationLoading = ref(false)
const llm2HydrationError = ref<string | null>(null)

const messagesPayload = computed<ChatHistoryParamPayload | undefined>(() => {
  return props.flowAction.actionParams.messages;
});

const isLlm2Format = computed(() => isLlm2ChatHistoryWrapper(messagesPayload.value));

const legacyMessages = computed(() => {
  const payload = messagesPayload.value;
  if (!payload || isLlm2ChatHistoryWrapper(payload)) {
    return [];
  }
  payload.forEach(addParsedArguments);
  return payload;
});

const hydratedLlm2Messages = computed(() => llm2Messages.value || []);

// Watch for llm2 payloads and hydrate
watch(
  () => [props.flowAction.workspaceId, props.flowAction.flowId, messagesPayload.value] as const,
  async ([actionWorkspaceId, actionFlowId, payload]) => {
    if (!isLlm2ChatHistoryWrapper(payload)) {
      llm2Messages.value = null;
      llm2HydrationLoading.value = false;
      llm2HydrationError.value = null;
      return;
    }

    const workspaceId = payload.workspaceId || actionWorkspaceId;
    const flowId = payload.flowId || actionFlowId;

    llm2HydrationLoading.value = true;
    llm2HydrationError.value = null;

    try {
      const response = await fetch(`/api/v1/workspaces/${workspaceId}/flows/${flowId}/chat_history/hydrate`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ refs: payload.refs }),
      });

      if (!response.ok) {
        throw new Error(`Hydration failed: ${response.status} ${response.statusText}`);
      }

      const data = await response.json();
      llm2Messages.value = data.messages || [];
    } catch (e: any) {
      llm2HydrationError.value = e.message || 'Failed to hydrate chat history';
      llm2Messages.value = null;
    } finally {
      llm2HydrationLoading.value = false;
    }
  },
  { immediate: true }
);

function parseLlm2ToolArguments(args: string): object {
  try {
    return JSON.parse(args) as object;
  } catch {
    return { raw: args };
  }
}

function getTextBlockText(block: Llm2ContentBlock): string {
  return block.type === 'text' ? block.text : '';
}

function getToolUseBlock(block: Llm2ContentBlock) {
  return block.type === 'tool_use' ? block.toolUse : null;
}

function getToolResultBlock(block: Llm2ContentBlock) {
  return block.type === 'tool_result' ? block.toolResult : null;
}

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

const isLlm2Response = computed(() => {
  const result = parsedActionResult.value;
  return result && result.output && Array.isArray(result.output.content);
});

const llm2ResponseBlocks = computed(() => {
  const result = parsedActionResult.value;
  if (!result?.output?.content) return [];
  return result.output.content as Array<Record<string, any>>;
});

const effectiveModel = computed(() => props.flowAction.actionParams.model || parsedActionResult.value?.model || completion.value?.model || '')
const effectiveProvider = computed(() => props.flowAction.actionParams.provider || parsedActionResult.value?.provider || completion.value?.provider || '')

const usage = computed<Usage | null>(() => parsedActionResult.value?.usage || completion.value?.usage || null)

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

.llm2-loading {
  padding: 1rem;
  color: var(--color-text-2);
  font-style: italic;
}

.llm2-error {
  padding: 1rem;
  color: var(--color-error, #c00);
  background: var(--color-error-bg, rgba(200, 0, 0, 0.1));
  border-radius: 4px;
  margin-bottom: 1rem;
}

.llm2-blocks {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.llm2-text-block {
  margin-bottom: 0.5rem;
}

.llm2-tool-use-block {
  margin-bottom: 0.5rem;
}

.llm2-tool-result-block {
  margin-bottom: 0.5rem;
  padding-left: 0.5rem;
  border-left: 2px solid var(--color-border-contrast);
}

.tool-result-header {
  font-weight: bold;
  margin-bottom: 0.25rem;
}

.tool-result-error {
  color: var(--color-error, #c00);
}

.tool-result-text {
  margin: 0;
  padding: 0.5rem;
  background: var(--color-background-soft);
  border-radius: 4px;
  white-space: pre-wrap;
  word-break: break-word;
}

.llm2-unknown-block {
  margin-bottom: 0.5rem;
  padding: 0.5rem;
  background: var(--color-background-soft);
  border-radius: 4px;
}

.unknown-block-header {
  font-weight: bold;
  color: var(--color-text-2);
  margin-bottom: 0.25rem;
}

</style>