<template>
  <div class="dev-run-controls">
    <div class="dev-run-header">
      <span class="dev-run-label">Dev Run</span>
      <button
        v-if="!isRunning"
        type="button"
        class="dev-run-button start"
        :disabled="disabled"
        @click="handleStart"
      >
        Start
      </button>
      <button
        v-else
        type="button"
        class="dev-run-button stop"
        :disabled="disabled"
        @click="handleStop"
      >
        Stop
      </button>
      <button
        v-if="isRunning || hasOutput"
        type="button"
        class="dev-run-toggle"
        @click="toggleOutput"
      >
        {{ showOutput ? 'Hide Output' : 'Show Output' }}
      </button>
    </div>
    <div v-if="showOutput && (isRunning || hasOutput)" class="dev-run-output">
      <pre ref="outputRef">{{ outputText }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue';

interface DevRunStartedEvent {
  eventType: 'dev_run_started';
  flowId: string;
  devRunId: string;
  commandSummary: string;
  workingDir: string;
  pid: number;
  pgid: number;
}

interface DevRunOutputEvent {
  eventType: 'dev_run_output';
  devRunId: string;
  stream: 'stdout' | 'stderr';
  chunk: string;
  sequence: number;
  timestamp: number;
}

interface DevRunEndedEvent {
  eventType: 'dev_run_ended';
  flowId: string;
  devRunId: string;
  exitStatus?: number;
  signal?: string;
  error?: string;
}

interface EndStreamEvent {
  eventType: 'end_stream';
  parentId: string;
}

type DevRunFlowEvent = DevRunStartedEvent | DevRunOutputEvent | DevRunEndedEvent | EndStreamEvent;

const props = defineProps<{
  workspaceId: string;
  flowId: string;
  disabled?: boolean;
}>();

const emit = defineEmits<{
  (e: 'start'): void;
  (e: 'stop'): void;
}>();

const isRunning = ref(false);
const currentDevRunId = ref<string | null>(null);
const showOutput = ref(false);
const outputLines = ref<string[]>([]);
const outputRef = ref<HTMLPreElement | null>(null);

let flowEventsSocket: WebSocket | null = null;
let outputEventsSocket: WebSocket | null = null;
let flowSocketClosed = false;
let outputSocketClosed = false;

const outputText = computed(() => outputLines.value.join(''));
const hasOutput = computed(() => outputLines.value.length > 0);

function handleStart() {
  emit('start');
}

function handleStop() {
  emit('stop');
}

function toggleOutput() {
  showOutput.value = !showOutput.value;
}

function getWebSocketUrl(path: string): string {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${protocol}//${window.location.host}${path}`;
}

function connectFlowEventsSocket() {
  if (flowEventsSocket) return;
  
  flowSocketClosed = false;
  flowEventsSocket = new WebSocket(
    getWebSocketUrl(`/ws/v1/workspaces/${props.workspaceId}/flows/${props.flowId}/events`)
  );

  flowEventsSocket.onopen = () => {
    flowEventsSocket?.send(JSON.stringify({ parentId: props.flowId }));
  };

  flowEventsSocket.onmessage = (event) => {
    try {
      const flowEvent = JSON.parse(event.data) as DevRunFlowEvent;
      handleFlowEvent(flowEvent);
    } catch (err) {
      console.error('Error parsing flow event:', err);
    }
  };

  flowEventsSocket.onerror = (error) => {
    console.error('Flow events WebSocket error:', error);
  };

  flowEventsSocket.onclose = () => {
    if (flowSocketClosed) return;
    setTimeout(() => {
      if (!flowSocketClosed) {
        flowEventsSocket = null;
        connectFlowEventsSocket();
      }
    }, 1000);
  };
}

let startedDebounceTimer = ref<NodeJS.Timeout | null>(null);

function handleFlowEvent(event: DevRunFlowEvent) {
  switch (event.eventType) {
    case 'dev_run_started':
      isRunning.value = true;
      currentDevRunId.value = event.devRunId;
      outputLines.value = [];

      // connect whether or not showing output, so showing it is instant
      // but debounce so we don't connect to previous dev runs that ended already
      if (startedDebounceTimer.value) {
        clearTimeout(startedDebounceTimer.value);
      }
      startedDebounceTimer.value = setTimeout(() => {
        if (currentDevRunId.value === event.devRunId && isRunning.value) {
          connectOutputEventsSocket(event.devRunId);
        }
      }, 250);
      break;
    case 'dev_run_ended':
      if (startedDebounceTimer.value) {
        clearTimeout(startedDebounceTimer.value);
      }
      isRunning.value = false;
      disconnectOutputEventsSocket();
      break;
  }
}

function connectOutputEventsSocket(devRunId: string) {
  if (outputEventsSocket) {
    // already connected, no need to connect again
    return
  }

  outputSocketClosed = false;
  outputEventsSocket = new WebSocket(
    getWebSocketUrl(`/ws/v1/workspaces/${props.workspaceId}/flows/${props.flowId}/events`)
  );

  outputEventsSocket.onopen = () => {
    outputEventsSocket?.send(JSON.stringify({ parentId: devRunId }));
  };

  outputEventsSocket.onmessage = (event) => {
    try {
      const flowEvent = JSON.parse(event.data) as DevRunFlowEvent;
      if (flowEvent.eventType === 'dev_run_output') {
        outputLines.value.push(flowEvent.chunk);
        scrollOutputToBottom();
      } else if (flowEvent.eventType === 'end_stream') {
        disconnectOutputEventsSocket();
      }
    } catch (err) {
      console.error('Error parsing output event:', err);
    }
  };

  outputEventsSocket.onerror = (error) => {
    console.error('Output events WebSocket error:', error);
  };

  outputEventsSocket.onclose = () => {
    if (outputSocketClosed) return;
    setTimeout(() => {
      if (!outputSocketClosed && currentDevRunId.value === devRunId && isRunning.value) {
        outputEventsSocket = null;
        connectOutputEventsSocket(devRunId);
      }
    }, 1000);
  };
}

function disconnectOutputEventsSocket() {
  outputSocketClosed = true;
  if (outputEventsSocket) {
    outputEventsSocket.close();
    outputEventsSocket = null;
  }
}

function scrollOutputToBottom() {
  if (outputRef.value) {
    outputRef.value.scrollTop = outputRef.value.scrollHeight;
  }
}

watch(() => props.flowId, () => {
  disconnectFlowEventsSocket();
  isRunning.value = false;
  currentDevRunId.value = null;
  outputLines.value = [];
  connectFlowEventsSocket();
}, { immediate: true });

function disconnectFlowEventsSocket() {
  flowSocketClosed = true;
  if (flowEventsSocket) {
    flowEventsSocket.close();
    flowEventsSocket = null;
  }
}

onUnmounted(() => {
  disconnectFlowEventsSocket();
  disconnectOutputEventsSocket();
});
</script>

<style scoped>
.dev-run-controls {
  margin: 1rem 0;
  border: 1px solid var(--color-border);
  border-radius: 4px;
  padding: 0.75rem;
  background-color: var(--color-background-soft);
}

.dev-run-header {
  display: flex;
  align-items: center;
  gap: 0.75rem;
}

.dev-run-label {
  font-weight: 500;
  color: var(--color-text);
}

.dev-run-button {
  padding: 0.375rem 0.75rem;
  border: none;
  border-radius: 4px;
  cursor: pointer;
  font-size: 0.875rem;
  color: white;
  text-shadow: 1px 1px 1px rgba(0, 0, 0, 0.3);
}

.dev-run-button.start {
  background-color: var(--color-primary, #4a9eff);
}

.dev-run-button.stop {
  background-color: #dc3545;
}

.dev-run-button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.dev-run-toggle {
  padding: 0.375rem 0.75rem;
  border: 1px solid var(--color-border);
  border-radius: 4px;
  cursor: pointer;
  font-size: 0.875rem;
  background-color: var(--color-background);
  color: var(--color-text);
}

.dev-run-toggle:hover {
  background-color: var(--color-background-mute);
}

.dev-run-output {
  margin-top: 0.75rem;
  max-height: 20rem;
  overflow: auto;
  background-color: var(--color-background);
  border: 1px solid var(--color-border);
  border-radius: 4px;
}

.dev-run-output pre {
  margin: 0;
  padding: 0.5rem;
  font-family: monospace;
  font-size: 0.8125rem;
  white-space: pre-wrap;
  word-break: break-all;
  color: var(--color-text);
}
</style>