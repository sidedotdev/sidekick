<template>
  <div v-if="flow">
    <FlowEditorLinks
      v-if="!embedded"
      :flow-id="flow.id"
      :worktrees="flow.worktrees"
      :show-model-configuration="showModelConfiguration"
      @model-configuration="modelConfigModalOpen = true"
    />
    <FlowEditorLinks
      v-else
      :flow-id="flow.id"
      :worktrees="flow.worktrees"
      :show-model-configuration="showModelConfiguration"
      subtask
      @model-configuration="modelConfigModalOpen = true"
    />
    <FlowModelConfigModal
      v-if="modelConfigModalOpen"
      :workspace-id="flow.workspaceId"
      :flow-id="flow.id"
      @close="modelConfigModalOpen = false"
    />
    <!-- TODO: In the future, we should allow going to next step even if currently paused -->
    <div 
      v-if="flow && !['completed', 'failed', 'canceled', 'paused'].includes(flow.status)" 
      class="flow-controls-container"
    >
      <button @click="pauseFlow" class="pause-button">⏸︎</button>

      <!--
        NOTE: goToNextStep is only avaiable in devMode for now, until it has
        been further tested, since this is a major new feature and is likely
        somewhat buggy now. devMode condition will be removed by sidekick
        maintainers in the future, when it's ready to be released.
        This is a purposeful change from the original requirements/step
        definition, guided by the user who provided that.
      -->
      <button 
        v-if="devMode && isGoNextAvailable"
        @click="goToNextStep"
        class="next-button"
      >
        ⏭
      </button>
    </div>
  </div>
  <div ref="flowActionsContainerRef" class="flow-actions-container" :class="{ 'short-content': shortContent }">
    <div ref="scrollContainerRef" class="scroll-container">
      <div v-if="isLoadingFlow && !flow" class="loading-indicator">Loading...</div>
      <div v-else-if="flow && isStartingFlow" class="loading-indicator">Starting Task...</div>
      <button v-if="hasOlderHistory" :disabled="isLoadingHistory" @click="loadOlderHistory">
        {{ isLoadingHistory ? 'Loading history…' : 'Load older history' }}
      </button>
      <p v-if="historyError" role="alert">{{ historyError }}</p>
      <SubflowContainer
        v-for="subflowTree in subflowTrees"
        :key="subflowTree.id"
        :subflowTree="subflowTree"
        :defaultExpanded="containsFlowAction(subflowTree, latestActionId)"
        :latestActionId="latestActionId"
        :subflowsById="subflowsById"
      />
    </div>
  </div>

  <IdeSelectorDialog
    :show="showIdeSelector"
    @select="selectIde"
    @cancel="cancelIdeSelection"
  />
</template>

<script setup lang="ts">
import { computed, onMounted, ref, onUnmounted, watch, provide } from 'vue'
import { useEventBus } from '@vueuse/core'
import SubflowContainer from '@/components/SubflowContainer.vue'
import IdeSelectorDialog from '@/components/IdeSelectorDialog.vue'
import FlowEditorLinks from '@/components/FlowEditorLinks.vue'
import FlowModelConfigModal from '@/components/FlowModelConfigModal.vue'
import type { FlowAction, SubflowTree, ChatMessageDelta, Flow, Subflow, Workspace, Llm2StreamEvent } from '../lib/models'
import { SubflowStatus } from '../lib/models'
import { Llm2ActionStream, type Llm2StreamSnapshot } from '../lib/llm2ActionStream'
import { buildSubflowTrees, containsFlowAction } from '../lib/subflow'
import { createFlowHistoryPager } from '../lib/flowHistoryPager'
import { useRoute, useRouter } from 'vue-router'
import { store } from '../lib/store'
import { isKanbanKeyboardControlEnabled } from '../lib/localSettings'
import { USER_REQUEST_SHORTCUT_SUBMITTED_KEY, isUserRequestAction } from '../lib/userRequestShortcut'
import { viewCache } from '../lib/viewCache'
import { isFlowModelConfigVisible } from '../lib/flowModelConfig'
import { useIdeOpener, IDE_OPENER_KEY } from '@/composables/useIdeOpener'

const props = defineProps<{
  flowId?: string
  embedded?: boolean
}>()

const emit = defineEmits<{
  // Embedded only: the last pending user request was submitted via keyboard shortcut
  (e: 'userRequestsCleared'): void
}>()

const subflowProcessingDebounceTimers = ref<Record<string, NodeJS.Timeout>>({})
const workspace = ref<Workspace | null>(null)

const { showIdeSelector, openInIde, selectIde, cancelIdeSelection } = useIdeOpener()

const handleOpenInIde = (relativePath: string, lineNumber?: number | null) => {
  const baseDir = flow.value?.worktrees?.[0]?.workingDirectory || workspace.value?.localRepoDir
  if (!baseDir) {
    alert('Error: No base directory available for opening file in IDE')
    return
  }
  const absolutePath = `${baseDir}/${relativePath}`
  openInIde(absolutePath, lineNumber, baseDir)
}

provide(IDE_OPENER_KEY, handleOpenInIde)
const devMode = computed(() => import.meta.env.MODE === 'development')
const modelConfigModalOpen = ref(false)
const flowActions = ref<FlowAction[]>([])
const subflowTrees = ref<SubflowTree[]>([])
const route = useRoute()
const router = useRouter()

// With keyboard control on, submitting the flow's last pending request moves
// on to whatever needs attention next instead of idling on this flow.
const handleUserRequestShortcutSubmitted = (submitted: FlowAction) => {
  if (!isKanbanKeyboardControlEnabled()) return
  const otherRequestPending = flowActions.value.some(action =>
    action.id !== submitted.id && action.actionStatus === 'pending' && isUserRequestAction(action),
  )
  if (otherRequestPending) return
  if (props.embedded) {
    emit('userRequestsCleared')
  } else {
    router.push({ name: 'kanban' })
  }
}

provide(USER_REQUEST_SHORTCUT_SUBMITTED_KEY, handleUserRequestShortcutSubmitted)

const effectiveFlowId = computed(() => props.flowId ?? (route.params.id as string | undefined))

// activeDevStep: Stores IDs of 'step.dev', 'coding', or 'review_and_resolve' subflows that are currently active.
// This is populated by listening to WebSocket events for subflow status changes.
const activeDevStep = ref(new Set<string>());

// subflowsById: A record of subflow objects, keyed by their ID.

const updateActiveDevStep = (subflowId: string, subflowType: string | undefined, status: SubflowStatus) => {
  if (subflowType === 'step.dev' || subflowType === 'coding' || subflowType === 'review_and_resolve') {
    if (status === SubflowStatus.Started) {
      activeDevStep.value.add(subflowId);
    } else if (status === SubflowStatus.Complete || status === SubflowStatus.Failed) {
      activeDevStep.value.delete(subflowId);
    }
  }
};
// This is also populated by listening to WebSocket events for subflow status changes.
const subflowsById = ref<Record<string, Subflow>>({});

// isGoNextAvailable: A computed property determining the "Next" button's visibility.
// It's true if there's at least one active 'step.dev', 'coding', or 'review_and_resolve' subflow.
// This, combined with the main flow status check in the template (`!['completed', 'failed', 'canceled', 'paused'].includes(flow.status)`),
// fulfills the visibility conditions:
// 1. Main flow is active and not paused.
// 2. An active 'step.dev', 'coding', or 'review_and_resolve' subflow exists.
const isGoNextAvailable = computed(() => activeDevStep.value.size > 0);

const updateSubflowTrees = () => {
  const relevantFlowActions = flowActions.value.filter(
    (action) => action.actionType !== 'user_request.continue' || action.actionStatus === 'pending'
  )
  subflowTrees.value = buildSubflowTrees(relevantFlowActions, subflowsById.value)
}

let flow = ref<Flow | null>(null)
const showModelConfiguration = computed(
  () => !!flow.value && isFlowModelConfigVisible(flow.value.status, devMode.value),
)
const flowActionsContainerRef = ref<HTMLDivElement | null>(null)
const scrollContainerRef = ref<HTMLDivElement | null>(null)
let actionChangesSocket: WebSocket | null = null
let actionChangesSocketClosed = false
let eventsSocket: WebSocket | null = null
let eventsSocketClosed = false;
let pendingEventSubscriptions: string[] = [];
// Per-flow-action llm2 streaming state, keyed by flow action id. Kept outside
// the flow actions themselves since those get replaced by action-change updates.
let llm2ActionStreams = new Map<string, Llm2ActionStream>();
let shortContent = ref(true);
let currentFlowIdForSockets: string | null = null;
let isLoadingFlow = ref(false);
let isStartingFlow = ref(false);
let hasReceivedFirstAction = false;

let subflowTreeDebounceTimer: NodeJS.Timeout;
let subscribeStreamDebounceTimers: {[key: string]: NodeJS.Timeout} = {};
let subflowStatusUpdateDebounceTimers: {[key: string]: NodeJS.Timeout} = {};

const handleSubflowStatusUpdate = (subflowId: string, status: SubflowStatus, result?: string) => {
  clearTimeout(subflowStatusUpdateDebounceTimers[subflowId]);
  subflowStatusUpdateDebounceTimers[subflowId] = setTimeout(() => {
    const subflowToUpdate = subflowsById.value[subflowId];
    if (subflowToUpdate) {
      subflowToUpdate.status = status;
      if (result) {
        subflowToUpdate.result = result;
      }
      updateActiveDevStep(subflowId, subflowToUpdate.type, status);
    } else {
      getAndSubscribeSubflowWithDebounce(subflowId, 0);
    }
  }, 100);
};

const sendEventSubscription = (parentId: string) => {
  if (eventsSocket && eventsSocket.readyState === WebSocket.OPEN) {
    eventsSocket.send(JSON.stringify({ parentId }));
  } else {
    pendingEventSubscriptions.push(parentId);
  }
};

const applyLlm2StreamSnapshot = (flowActionId: string, snapshot: Llm2StreamSnapshot) => {
  const action = flowActions.value.find(a => a.id === flowActionId);
  if (!action) {
    console.error(`FlowAction with id ${flowActionId} not found.`);
    return;
  }
  action.streamingData = {
    content: action.streamingData?.content ?? '',
    toolCalls: action.streamingData?.toolCalls ?? [],
    blocks: snapshot.blocks,
    toolArguments: snapshot.toolArguments,
  };
};

const handleLlm2StreamEvent = (flowEvent: Llm2StreamEvent) => {
  const action = flowActions.value.find(a => a.id === flowEvent.flowActionId);
  if (!action) {
    console.error(`FlowAction with id ${flowEvent.flowActionId} not found.`);
    return;
  }
  // Events that trail the action's completion must not resurrect its disposed stream.
  if (action.actionStatus !== 'started') return;
  let stream = llm2ActionStreams.get(flowEvent.flowActionId);
  if (!stream) {
    stream = new Llm2ActionStream((snapshot) => applyLlm2StreamSnapshot(flowEvent.flowActionId, snapshot));
    llm2ActionStreams.set(flowEvent.flowActionId, stream);
  }
  stream.applyEvent(flowEvent);
};

const disposeLlm2ActionStream = (flowActionId: string) => {
  llm2ActionStreams.get(flowActionId)?.dispose();
  llm2ActionStreams.delete(flowActionId);
};

const disposeAllLlm2ActionStreams = () => {
  for (const stream of llm2ActionStreams.values()) {
    stream.dispose();
  }
  llm2ActionStreams.clear();
};

const connectEventsWebSocketForFlow = (flowId: string, initialFlowPromise?: Promise<Response>) => {
  const generation = historyGeneration
  const socket = new WebSocket(`ws://${window.location.host}/ws/v1/workspaces/${store.workspaceId}/flows/${flowId}/events`);
  eventsSocket = socket
  const isCurrent = () => generation === historyGeneration && socket === eventsSocket && !eventsSocketClosed

  socket.onopen = async () => {
    if (!isCurrent()) return
    console.log("Events WebSocket connection opened for flow:", flowId);
    if (initialFlowPromise) {
      try {
        await initialFlowPromise; // Wait for the main flow data to be fetched if provided
      } catch {
        // Live events can still be received if the metadata request failed.
      }
    }
    setTimeout(() => {
      if (!isCurrent()) return
      // Send message to subscribe to events for the main flow itself
      const message = JSON.stringify({parentId: flowId});
      eventsSocket?.send(message);

      // Process any pending subscriptions that were queued before the socket opened
      for (const parentId of pendingEventSubscriptions) {
        eventsSocket?.send(JSON.stringify({ parentId }));
      }
      pendingEventSubscriptions = [];
    }, 10);
  };

  socket.onmessage = (event) => {
    if (!isCurrent()) return
    try {
      setShortContent()
      const flowEvent = JSON.parse(event.data);
      switch (flowEvent.eventType) {
        case 'chat_message_delta': {
          const delta = flowEvent.chatMessageDelta as ChatMessageDelta;
          const actionIndex = flowActions.value.findIndex(action => action.id === flowEvent.flowActionId);
          if (actionIndex !== -1) {
            const action = flowActions.value[actionIndex];
            if (!action.streamingData) {
              action.streamingData = { content: '', toolCalls: [] };
            }
            if (delta.content) {
              action.streamingData.content += delta.content;
            }
            if (delta.toolCalls) {
              delta.toolCalls.forEach(toolCall => {
                const existingToolCall = action.streamingData!.toolCalls.find(tc => tc.id === toolCall.id);
                if (existingToolCall) {
                  if (toolCall.name) existingToolCall.name = toolCall.name;
                  if (toolCall.arguments) existingToolCall.arguments = (existingToolCall.arguments || '') + toolCall.arguments;
                } else {
                  action.streamingData!.toolCalls.push({
                    id: toolCall.id,
                    name: toolCall.name,
                    arguments: toolCall.arguments || ''
                  });
                }
              });
            }
            flowActions.value[actionIndex] = action;
          } else {
            console.error(`FlowAction with id ${flowEvent.flowActionId} not found.`);
          }
          break;
        }
        case 'llm2_stream_event': {
          handleLlm2StreamEvent(flowEvent as Llm2StreamEvent);
          break;
        }
        case 'status_change': {
          // Check if this status change is for the main flow
          if (flow.value && flowEvent.parentId === flow.value.id) {
            // Use targetId if present (new format), otherwise this is a flow status change
            if (flowEvent.targetId) {
              // This is a subflow status change with new format (parentId=flowId, targetId=subflowId)
              handleSubflowStatusUpdate(flowEvent.targetId, flowEvent.status, flowEvent.result);
            } else {
              // This is a flow status change
              flow.value.status = flowEvent.status;
            }
          } else { // This is a subflow status change (legacy format: parentId=subflowId)
            const subflowId = flowEvent.parentId;
            if (subflowId) { // Ensure subflowId is present
              handleSubflowStatusUpdate(subflowId, flowEvent.status, flowEvent.result);
            }
          }
          break;
        }
      }
    } catch (err) {
      console.error("Error parsing Events WebSocket message", err);
    }
  };

  socket.onerror = (error) => {
    if (isCurrent()) console.error("Events WebSocket error observed:", error);
  };

  socket.onclose = (event) => {
    if (!isCurrent()) return;
    console.log("Events WebSocket is closed. Reconnect will be attempted in 1 second.", event.reason);
    setTimeout(() => {
      if (isCurrent()) {
        connectEventsWebSocketForFlow(flowId); // Reconnect, no initialFlowPromise needed
      }
    }, 1000);
  };
};

const getAndSubscribeSubflowWithDebounce = (subflowId: string, delay: number, ancestors = new Set<string>()) => {
  if (ancestors.has(subflowId)) return
  const visited = new Set(ancestors).add(subflowId)
  const generation = historyGeneration
  clearTimeout(subflowProcessingDebounceTimers.value[subflowId])

  subflowProcessingDebounceTimers.value[subflowId] = setTimeout(async () => {
    try {
      let subflowData = subflowsById.value[subflowId]
      if (!subflowData) {
        const response = await fetch(`/api/v1/workspaces/${store.workspaceId}/subflows/${subflowId}`)
        if (!response.ok) throw new Error(`Failed to fetch subflow ${subflowId}: ${response.status}`)
        subflowData = (await response.json()).subflow as Subflow
        if (generation !== historyGeneration) return
        subflowsById.value[subflowId] = subflowData
        updateSubflowTrees()
      }
      if (generation !== historyGeneration) return
      updateActiveDevStep(subflowId, subflowData.type, subflowData.status)
      sendEventSubscription(subflowId)
      if (subflowData.parentSubflowId) {
        getAndSubscribeSubflowWithDebounce(subflowData.parentSubflowId, 10, visited)
      }
    } catch (err) {
      if (generation === historyGeneration) console.error(`Error processing subflow ${subflowId}:`, err)
    } finally {
      if (generation === historyGeneration) delete subflowProcessingDebounceTimers.value[subflowId]
    }
  }, delay)
}

const connectActionChangesWebSocketForFlow = (flowId: string) => {
  const generation = historyGeneration
  const socket = new WebSocket(`ws://${window.location.host}/ws/v1/workspaces/${store.workspaceId}/flows/${flowId}/action_changes_ws?streamMessageStartId=$`);
  actionChangesSocket = socket
  const isCurrent = () => generation === historyGeneration && socket === actionChangesSocket && !actionChangesSocketClosed

  socket.onopen = () => {
    if (isCurrent()) console.log("WebSocket connection opened for flow:", flowId);
  };

  socket.onmessage = async (event) => {
    if (!isCurrent()) return
    try {
      const flowAction: FlowAction = JSON.parse(event.data);

      // Handle first flow action
      if (!hasReceivedFirstAction) {
        hasReceivedFirstAction = true;
        isStartingFlow.value = false;

        // Reload flow data if no worktrees exist, as worktrees are created during flow execution
        if (flow.value && (!flow.value.worktrees || flow.value.worktrees.length === 0)) {
          try {
            const response = await fetch(`/api/v1/workspaces/${store.workspaceId}/flows/${flow.value.id}`);
            if (response.ok) {
              const flowData = await response.json();
              if (!isCurrent()) return
              flow.value = flowData.flow;
            }
          } catch (err) {
            console.error(`Error reloading flow data:`, err);
          }
        }
      }
      if (!isCurrent()) return

      // ensure we get subflow
      if (flowAction.subflowId) {
        getAndSubscribeSubflowWithDebounce(flowAction.subflowId, 100);
      }

      // Existing logic for flowAction processing
      clearTimeout(subscribeStreamDebounceTimers[flowAction.id]);
      subscribeStreamDebounceTimers[flowAction.id] = setTimeout(() => {
        if (!isCurrent()) return
        const latestFlowAction = flowActions.value.find((action) => action.id === flowAction.id);
        if (latestFlowAction?.actionStatus === 'started') {
          // Subscribe to events for the flow action itself (e.g., for chat messages)
          sendEventSubscription(flowAction.id);
        }
        setShortContent();
      }, 100);

      const index = flowActions.value.findIndex((action) => action.id === flowAction.id);
      if (index !== -1) {
        const existing = flowActions.value[index];
        if (new Date(flowAction.updated).getTime() < new Date(existing.updated).getTime()) {
          // events may be out of order at this point, even though the backend
          // provides them in order, if we do any awaits in the event handlers
          return;
        }
        // Action-change updates never carry streaming state, so carry over
        // what the events socket accumulated while the action is still running.
        if (flowAction.actionStatus === 'started' && existing.streamingData && !flowAction.streamingData) {
          flowAction.streamingData = existing.streamingData;
        }
        flowActions.value[index] = flowAction;
      } else {
        flowActions.value.push(flowAction);
      }
      if (flowAction.actionStatus !== 'started') {
        disposeLlm2ActionStream(flowAction.id);
      }

      clearTimeout(subflowTreeDebounceTimer);
      subflowTreeDebounceTimer = setTimeout(() => {
        if (isCurrent()) updateSubflowTrees();
      }, 100);
    } catch (err) {
      console.error("Error parsing WebSocket message for action changes", err);
    }
  };

  socket.onerror = (error) => {
    if (isCurrent()) console.error("ActionChanges WebSocket error observed:", error);
  };

  socket.onclose = (event) => {
    if (!isCurrent()) return;
    console.log("ActionChanges WebSocket is closed. Reconnect will be attempted in 1 second.", event.reason);
    setTimeout(() => {
      if (isCurrent()) connectActionChangesWebSocketForFlow(flowId);
    }, 1000);
  };
};

const actionHistory = createFlowHistoryPager<FlowAction>('actions', 'flowActions', fetchedActions => {
  const indices = new Map(flowActions.value.map((action, index) => [action.id, index]))
  for (const action of fetchedActions) {
    const index = indices.get(action.id)
    if (index !== undefined) {
      const existing = flowActions.value[index]
      if (new Date(action.updated).getTime() < new Date(existing.updated).getTime()) continue
      if (action.actionStatus === 'started' && existing.streamingData) {
        action.streamingData = existing.streamingData
      }
      flowActions.value[index] = action
    } else {
      indices.set(action.id, flowActions.value.length)
      flowActions.value.push(action)
    }
    if (action.subflowId) getAndSubscribeSubflowWithDebounce(action.subflowId, 100)
    if (action.actionStatus === 'started') sendEventSubscription(action.id)
    else disposeLlm2ActionStream(action.id)
  }
  if (fetchedActions.length > 0 && !hasReceivedFirstAction) {
    hasReceivedFirstAction = true
    isStartingFlow.value = false
  }
  updateSubflowTrees()
  setShortContent()
})

const subflowHistory = createFlowHistoryPager<Subflow>('subflows', 'subflows', subflows => {
  for (const subflow of subflows) {
    const existing = subflowsById.value[subflow.id]
    if (!existing || new Date(subflow.updated ?? 0).getTime() > new Date(existing.updated ?? 0).getTime()) {
      subflowsById.value[subflow.id] = subflow
    }
    const current = subflowsById.value[subflow.id]
    updateActiveDevStep(current.id, current.type, current.status)
    getAndSubscribeSubflowWithDebounce(current.id, 100)
  }
  updateSubflowTrees()
  setShortContent()
})

let historyGeneration = 0
const hasOlderHistory = computed(() =>
  !!effectiveFlowId.value && (actionHistory.hasMore.value || subflowHistory.hasMore.value),
)
const isLoadingHistory = computed(() => actionHistory.loading.value || subflowHistory.loading.value)
const historyError = computed(() => actionHistory.error.value || subflowHistory.error.value)
const latestActionId = computed(() => {
  const actions = flowActions.value.filter(action =>
    action.actionType !== 'user_request.continue' || action.actionStatus === 'pending',
  )
  return actions.reduce<FlowAction | undefined>((latest, action) => {
    if (!latest) return action
    const difference = new Date(action.created).getTime() - new Date(latest.created).getTime()
    return difference > 0 || (difference === 0 && action.id > latest.id) ? action : latest
  }, undefined)?.id
})

const loadOlderHistory = async () => {
  await Promise.all([actionHistory.load(), subflowHistory.load()])
}

const saveCurrentFlowToCache = () => {
  if (flow.value && currentFlowIdForSockets) {
    viewCache.setFlowView(currentFlowIdForSockets, {
      flow: flow.value,
      workspace: workspace.value,
    })
  }
}

const setupFlow = async (newFlowId: string | undefined) => {
  saveCurrentFlowToCache();
  const generation = ++historyGeneration
  actionHistory.reset(store.workspaceId ?? undefined, newFlowId)
  subflowHistory.reset(store.workspaceId ?? undefined, newFlowId)

  // Close existing WebSockets first
  if (actionChangesSocket) {
    actionChangesSocketClosed = true;
    actionChangesSocket.close();
    actionChangesSocket = null;
  }
  if (eventsSocket) {
    eventsSocketClosed = true;
    eventsSocket.close();
    eventsSocket = null;
  }
  actionChangesSocketClosed = false; // Reset for new connections
  eventsSocketClosed = false;
  pendingEventSubscriptions = [];
  disposeAllLlm2ActionStreams();
  currentFlowIdForSockets = null; // Reset

  if (!newFlowId) {
    flow.value = null;
    flowActions.value = [];
    activeDevStep.value.clear();
    subflowsById.value = {};
    isLoadingFlow.value = false;
    isStartingFlow.value = false;
    hasReceivedFirstAction = false;
    // Clear any pending subflow status update timers
    Object.keys(subflowStatusUpdateDebounceTimers).forEach(key => {
      clearTimeout(subflowStatusUpdateDebounceTimers[key]);
    });
    subflowStatusUpdateDebounceTimers = {};

    // Clear any pending subflow processing timers
    Object.values(subflowProcessingDebounceTimers.value).forEach(timerId => clearTimeout(timerId));
    subflowProcessingDebounceTimers.value = {};

    updateSubflowTrees(); // Clear trees
    return;
  }

  currentFlowIdForSockets = newFlowId;
  isStartingFlow.value = false;
  hasReceivedFirstAction = false;

  // Clear any pending subflow status update timers
  Object.keys(subflowStatusUpdateDebounceTimers).forEach(key => {
    clearTimeout(subflowStatusUpdateDebounceTimers[key]);
  });
  subflowStatusUpdateDebounceTimers = {};

  // Clear any pending subflow processing timers
  Object.values(subflowProcessingDebounceTimers.value).forEach(timerId => clearTimeout(timerId));
  subflowProcessingDebounceTimers.value = {};

  // Reload history from the newest page to avoid gaps after time away from a flow.
  const cached = viewCache.getFlowView(newFlowId);
  flow.value = cached?.flow ?? null
  workspace.value = cached?.workspace ?? null
  flowActions.value = []
  subflowsById.value = {}
  activeDevStep.value.clear()
  isLoadingFlow.value = !cached
  updateSubflowTrees()

  const flowPromise = fetch(`/api/v1/workspaces/${store.workspaceId}/flows/${newFlowId}`);

  connectEventsWebSocketForFlow(newFlowId, flowPromise);
  connectActionChangesWebSocketForFlow(newFlowId);

  void loadOlderHistory()

  try {
    const response = await flowPromise;
    if (generation !== historyGeneration) return
    if (response.ok) {
      const flowData = await response.json();
      if (generation !== historyGeneration) return
      flow.value = flowData.flow;
      isLoadingFlow.value = false;
      isStartingFlow.value = !hasReceivedFirstAction;

      // Fetch workspace for localRepoDir fallback
      if (flow.value?.workspaceId) {
        try {
          const wsResponse = await fetch(`/api/v1/workspaces/${flow.value.workspaceId}`);
          if (wsResponse.ok) {
            const wsData = await wsResponse.json();
            if (generation !== historyGeneration) return
            workspace.value = wsData.workspace;
          }
        } catch (wsErr) {
          console.error('Error fetching workspace:', wsErr);
        }
      }
    } else {
      console.error(`Failed to fetch flow ${newFlowId}:`, await response.text());
      flow.value = null;
      isLoadingFlow.value = false;
      isStartingFlow.value = false;
    }
  } catch (err) {
    if (generation !== historyGeneration) return
    console.error(`Error fetching flow ${newFlowId}:`, err);
    flow.value = null;
    isLoadingFlow.value = false;
    isStartingFlow.value = false;
  }
  setShortContent();
};


onMounted(async () => {
  const initialFlowId = effectiveFlowId.value;
  if (initialFlowId) {
    await setupFlow(initialFlowId);
  }
});

watch(effectiveFlowId, async (newFlowId, oldFlowId) => {
  if (newFlowId && newFlowId !== oldFlowId && typeof newFlowId === 'string') {
    await setupFlow(newFlowId);
  } else if (!newFlowId && oldFlowId) { // Navigating away from a specific flow
    await setupFlow(undefined);
  }
});

const goToNextStep = async () => {
  if (!flow.value) return;
  try {
    const response = await fetch(`/api/v1/workspaces/${store.workspaceId}/flows/${flow.value.id}/user_action`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ actionType: 'go_next_step' }),
    });
    if (!response.ok) {
      console.error(`Failed to trigger next step: ${response.status}`, await response.text());
    } else {
      console.log('Next step triggered successfully');
    }
  } catch (err) {
    console.error('Error triggering next step:', err);
  }
};

const contentFitsContainer = () => {
  const contentHeight = scrollContainerRef.value?.scrollHeight || 0
  const containerHeight = flowActionsContainerRef.value?.clientHeight || 0
  return contentHeight <= containerHeight
}

// HACK: run fast to get immediate feedback, but slower too when rendering takes longer
const afterRender = (fn: () => void) => {
  setTimeout(fn, 10)
  setTimeout(fn, 50)
  setTimeout(fn, 250)
}

let setShortContent = () => {
  afterRender(() => {
    shortContent.value = contentFitsContainer()
  })
}

// Flipping flex-direction while content overflows resets the scroll position,
// so a collapse may only switch to the short layout once nothing can scroll.
useEventBus('flow-view-collapse').on(() => {
  afterRender(() => {
    if (contentFitsContainer()) {
      shortContent.value = true
    }
  })
})

const pauseFlow = async () => {
  if (!flow.value) return
  try {
    await fetch(`/api/v1/workspaces/${store.workspaceId}/flows/${flow.value.id}/pause`, {
      method: 'POST',
    })
  } catch (err) {
    console.error('Failed to pause flow:', err)
  }
}

onUnmounted(() => {
  saveCurrentFlowToCache();
  historyGeneration++
  actionHistory.reset()
  subflowHistory.reset()
  Object.values(subflowProcessingDebounceTimers.value).forEach(clearTimeout)
  clearTimeout(subflowTreeDebounceTimer)
  actionChangesSocketClosed = true; // Prevent reconnects
  if (actionChangesSocket) {
    actionChangesSocket.close();
  }
  eventsSocketClosed = true; // Prevent reconnects
  if (eventsSocket) {
    eventsSocket.close();
  }
  disposeAllLlm2ActionStreams();
  // Clear any pending subflow status update timers on unmount
  Object.keys(subflowStatusUpdateDebounceTimers).forEach(key => {
    clearTimeout(subflowStatusUpdateDebounceTimers[key]);
  });
  subflowStatusUpdateDebounceTimers = {};
})
</script>


<style scoped>
.flow-actions-container {
  width: 100%;
  height: 100%;
  padding: 0 0 1rem;
  overflow: auto;
  display: flex;
  flex-direction: column-reverse;
}

.flow-actions-container.short-content {
  flex-direction: column;
}

.flow-controls-container { /* Renamed from pause-button-container */
  position: absolute;
  right: 1.5rem;
  bottom: 1.5rem;
  display: flex;
  align-items: center; /* Align items vertically */
  gap: 0.5rem; /* Add space between buttons */
  justify-content: center;
  padding: 0.75rem;
  z-index: 1000;
}

.pause-button {
  padding: 0.25rem 2rem;
  border-radius: 0.5rem;
  opacity: 0.8;
  background-color: var(--color-primary);
  color: var(--vp-c-text-1);
  border: 1px solid var(--vp-c-divider);
  cursor: pointer;
  font-size: 3.5rem;
  transition: opacity 0.2s;
}

.pause-button:hover {
  opacity: 1;
}

.next-button {
  padding: 0.5rem 1rem;
  border-radius: 0.5rem;
  opacity: 0.8;
  background-color: var(--color-primary); /* Using primary color, can be adjusted */
  color: var(--vp-c-text-1);
  border: 1px solid var(--vp-c-divider);
  cursor: pointer;
  font-size: 1rem; /* Standard font size for text button */
  transition: opacity 0.2s;
}

.next-button:hover {
  opacity: 1;
}

.loading-indicator {
  display: flex;
  justify-content: center;
  align-items: center;
  padding: 2rem;
  color: var(--vp-c-text-2);
  font-size: 1rem;
  font-style: italic;
}

</style>