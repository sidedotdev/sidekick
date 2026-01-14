<template>
  <div class="dev-run-controls">
    <div class="dev-run-header">
      <span class="dev-run-label">Dev Run</span>
      <button
        v-if="!hasActiveRuns"
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
        v-if="hasActiveRuns || hasOutput"
        type="button"
        class="dev-run-toggle"
        @click="toggleOutput"
      >
        {{ showOutput ? 'Hide Output' : 'Show Output' }}
      </button>
    </div>
    <div v-if="showOutput && (hasActiveRuns || hasOutput)" class="dev-run-output">
      <pre ref="outputRef">{{ outputText }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onUnmounted, onMounted } from 'vue';

interface DevRunInstance {
  devRunId: string;
  sessionId: number;
  outputFilePath: string;
  commandId: string;
}

interface DevRunState {
  activeRuns: Record<string, DevRunInstance>;
}

interface DevRunStartedEvent {
  eventType: 'dev_run_started';
  flowId: string;
  devRunId: string;
  commandId: string;
  commandSummary: string;
  workingDir: string;
  pid: number;
  sessionId: number;
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
  commandId: string;
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

// Track active dev runs by command ID
const activeRuns = ref<Map<string, DevRunInstance>>(new Map());
const showOutput = ref(false);
const outputLines = ref<string[]>([]);
const outputRef = ref<HTMLPreElement | null>(null);

let flowEventsSocket: WebSocket | null = null;
// Track output sockets per dev run ID
const outputEventsSockets = new Map<string, WebSocket>();
let flowSocketClosed = false;
const outputSocketsClosed = new Set<string>();

const outputText = computed(() => outputLines.value.join(''));
const hasOutput = computed(() => outputLines.value.length > 0);
const hasActiveRuns = computed(() => activeRuns.value.size > 0);

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

async function queryDevRunState(): Promise<DevRunState | null> {
  try {
    const response = await fetch(`/api/v1/workspaces/${props.workspaceId}/flows/${props.flowId}/query`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ query: 'dev_run_state' })
    });
    if (!response.ok) {
      return null;
    }
    const data = await response.json();
    return data.result as DevRunState;
  } catch (err) {
    console.debug('Dev run state query error:', err);
    return null;
  }
}

async function recoverActiveRuns() {
  const state = await queryDevRunState();
  if (!state?.activeRuns) return;

  for (const [commandId, instance] of Object.entries(state.activeRuns)) {
    activeRuns.value.set(commandId, instance);
    connectOutputEventsSocket(instance.devRunId);
  }
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

const startedDebounceTimers = new Map<string, NodeJS.Timeout>();

function handleFlowEvent(event: DevRunFlowEvent) {
  switch (event.eventType) {
    case 'dev_run_started': {
      const instance: DevRunInstance = {
        devRunId: event.devRunId,
        sessionId: event.sessionId,
        outputFilePath: '',
        commandId: event.commandId,
      };
      activeRuns.value.set(event.commandId, instance);

      // Debounce connection to avoid connecting to runs that end immediately
      const existingTimer = startedDebounceTimers.get(event.commandId);
      if (existingTimer) {
        clearTimeout(existingTimer);
      }
      startedDebounceTimers.set(event.commandId, setTimeout(() => {
        if (activeRuns.value.has(event.commandId)) {
          connectOutputEventsSocket(event.devRunId);
        }
        startedDebounceTimers.delete(event.commandId);
      }, 250));
      break;
    }
    case 'dev_run_ended': {
      const commandId = event.commandId;
      const timer = startedDebounceTimers.get(commandId);
      if (timer) {
        clearTimeout(timer);
        startedDebounceTimers.delete(commandId);
      }
      activeRuns.value.delete(commandId);
      disconnectOutputEventsSocket(event.devRunId);
      break;
    }
  }
}

function connectOutputEventsSocket(devRunId: string) {
  if (outputEventsSockets.has(devRunId)) {
    return;
  }

  outputSocketsClosed.delete(devRunId);
  const socket = new WebSocket(
    getWebSocketUrl(`/ws/v1/workspaces/${props.workspaceId}/flows/${props.flowId}/events`)
  );
  outputEventsSockets.set(devRunId, socket);

  socket.onopen = () => {
    socket.send(JSON.stringify({ parentId: devRunId }));
  };

  socket.onmessage = (event) => {
    try {
      const flowEvent = JSON.parse(event.data) as DevRunFlowEvent;
      if (flowEvent.eventType === 'dev_run_output') {
        outputLines.value.push(flowEvent.chunk);
        scrollOutputToBottom();
      } else if (flowEvent.eventType === 'end_stream') {
        disconnectOutputEventsSocket(devRunId);
      }
    } catch (err) {
      console.error('Error parsing output event:', err);
    }
  };

  socket.onerror = (error) => {
    console.error('Output events WebSocket error:', error);
  };

  socket.onclose = () => {
    if (outputSocketsClosed.has(devRunId)) return;
    outputEventsSockets.delete(devRunId);
    setTimeout(() => {
      // Reconnect if the run is still active
      const isStillActive = Array.from(activeRuns.value.values()).some(r => r.devRunId === devRunId);
      if (!outputSocketsClosed.has(devRunId) && isStillActive) {
        connectOutputEventsSocket(devRunId);
      }
    }, 1000);
  };
}

function disconnectOutputEventsSocket(devRunId: string) {
  outputSocketsClosed.add(devRunId);
  const socket = outputEventsSockets.get(devRunId);
  if (socket) {
    socket.close();
    outputEventsSockets.delete(devRunId);
  }
}

function disconnectAllOutputEventsSockets() {
  for (const devRunId of outputEventsSockets.keys()) {
    disconnectOutputEventsSocket(devRunId);
  }
}

function scrollOutputToBottom() {
  if (outputRef.value) {
    outputRef.value.scrollTop = outputRef.value.scrollHeight;
  }
}

watch(() => props.flowId, async () => {
  disconnectFlowEventsSocket();
  disconnectAllOutputEventsSockets();
  activeRuns.value.clear();
  outputLines.value = [];
  connectFlowEventsSocket();
  await recoverActiveRuns();
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
  disconnectAllOutputEventsSockets();
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