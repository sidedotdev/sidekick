<template>
  <div v-if="flow">
    <div class="editor-links">
      <p v-for="worktree in flow.worktrees" :key="worktree.id">
        Open Worktree
        <a :href="`vscode://file/${worktree.workingDirectory}?windowId=_blank`">
          <VSCodeIcon/>
        </a>&nbsp;<a :href="`idea://open?file=${encodeURIComponent(worktree.workingDirectory)}`">
          <IntellijIcon/>
        </a>
      </p>
      <div class="debug" v-if="devMode">
        <a :href="`http://localhost:19855/namespaces/default/workflows/${flow.id}`">Temporal Flow</a>
      </div>
    </div>
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
  <div class="flow-actions-container" :class="{ 'short-content': shortContent }">
    <div class="scroll-container">
      <div v-if="isLoadingFlow && !flow" class="loading-indicator">Loading...</div>
      <div v-else-if="flow && isStartingFlow" class="loading-indicator">Starting Task...</div>
      <SubflowContainer v-for="(subflowTree, index) in subflowTrees" :key="index" :subflowTree="subflowTree" :defaultExpanded="index == subflowTrees.length - 1"/>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, onUnmounted, watch } from 'vue'
import { useEventBus } from '@vueuse/core'
import SubflowContainer from '@/components/SubflowContainer.vue'
import VSCodeIcon from '@/components/icons/VSCodeIcon.vue'
import IntellijIcon from '@/components/icons/IntellijIcon.vue'
import type { FlowAction, SubflowTree, ChatMessageDelta, Flow, Worktree, Subflow } from '../lib/models' // Added Subflow here
import { SubflowStatus } from '../lib/models'
import { buildSubflowTrees } from '../lib/subflow'
import { useRoute } from 'vue-router'
import { store } from '../lib/store'

const dataDir = `${import.meta.env.VITE_HOME}/Library/Application Support/sidekick` // FIXME switch to API call to backend

const subflowProcessingDebounceTimers = ref<Record<string, NodeJS.Timeout>>({})
const devMode = import.meta.env.MODE === 'development'
const flowActions = ref<FlowAction[]>([])
const subflowTrees = ref<SubflowTree[]>([])
const route = useRoute()

// activeDevStep: Stores IDs of 'step.dev', 'coding', or 'review_and_resolve' subflows that are currently active.
// This is populated by listening to WebSocket events for subflow status changes.
const activeDevStep = ref(new Set<string>());

// subflowsById: A record of subflow objects, keyed by their ID.
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
  const relevantFlowActions = flowActions.value
  const newSubtrees = buildSubflowTrees(relevantFlowActions)
  subflowTrees.value = newSubtrees
}

let flow = ref<Flow | null>(null)
let actionChangesSocket: WebSocket | null = null
let actionChangesSocketClosed = false
let eventsSocket: WebSocket | null = null
let eventsSocketClosed = false;
let shortContent = ref(true);
let currentFlowIdForSockets: string | null = null;
let isLoadingFlow = ref(false);
let isStartingFlow = ref(false);
let hasReceivedFirstAction = false;

let subflowTreeDebounceTimer: NodeJS.Timeout;
let subscribeStreamDebounceTimers: {[key: string]: NodeJS.Timeout} = {};
let subflowStatusUpdateDebounceTimers: {[key: string]: NodeJS.Timeout} = {};

const connectEventsWebSocketForFlow = (flowId: string, initialFlowPromise?: Promise<Response>) => {
  eventsSocket = new WebSocket(`ws://${window.location.host}/ws/v1/workspaces/${store.workspaceId}/flows/${flowId}/events`);

  eventsSocket.onopen = async () => {
    console.log("Events WebSocket connection opened for flow:", flowId);
    if (initialFlowPromise) {
      await initialFlowPromise; // Wait for the main flow data to be fetched if provided
    }
    setTimeout(() => {
      // Send message to subscribe to events for the main flow itself
      const message = JSON.stringify({parentId: flowId});
      eventsSocket?.send(message);
    }, 10);
  };

  eventsSocket.onmessage = (event) => {
    try {
      setShortContent()
      const flowEvent = JSON.parse(event.data);
      switch (flowEvent.eventType) {
        case 'chat_message_delta': {
          const delta = flowEvent.chatMessageDelta as ChatMessageDelta;
          const actionIndex = flowActions.value.findIndex(action => action.id === flowEvent.flowActionId);
          if (actionIndex !== -1) {
            const action = flowActions.value[actionIndex];
            const contentBuilder: string[] = [];
            if (delta.content) contentBuilder.push(delta.content);
            if (delta.toolCalls) {
              delta.toolCalls.forEach(toolCall => {
                if (toolCall.name) contentBuilder.push(`toolName = ${toolCall.name}\n`);
                if (toolCall.arguments) contentBuilder.push(toolCall.arguments);
              });
            }
            action.actionResult += contentBuilder.join('\n');
            flowActions.value[actionIndex] = action;
          } else {
            console.error(`FlowAction with id ${flowEvent.flowActionId} not found.`);
          }
          break;
        }
        case 'status_change': {
          // Check if this status change is for the main flow
          if (flow.value && flowEvent.parentId === flow.value.id) {
            flow.value.status = flowEvent.status;
          } else { // This is a subflow status change
            const subflowId = flowEvent.parentId;
            if (subflowId) { // Ensure subflowId is present
              clearTimeout(subflowStatusUpdateDebounceTimers[subflowId]);
              subflowStatusUpdateDebounceTimers[subflowId] = setTimeout(() => {
                const subflowToUpdate = subflowsById.value[subflowId];
                if (subflowToUpdate) {
                  subflowToUpdate.status = flowEvent.status; // Update status in our cache

                  if (subflowToUpdate.type === 'step.dev' || subflowToUpdate.type === 'coding' || subflowToUpdate.type === 'review_and_resolve') {
                    if (flowEvent.status === SubflowStatus.Started) {
                      activeDevStep.value.add(subflowId);
                    } else if (
                      flowEvent.status === SubflowStatus.Complete ||
                      flowEvent.status === SubflowStatus.Failed
                    ) {
                      activeDevStep.value.delete(subflowId);
                    }
                  }
                } else {
                  // Optional: Log if subflow not found, though it should have been fetched by actionChangesSocket
                  console.warn(`Received status update for subflow ${subflowId} not found in cache.`);
                }
              }, 100);
            }
          }
          break;
        }
      }
    } catch (err) {
      console.error("Error parsing Events WebSocket message", err);
    }
  };

  eventsSocket.onerror = (error) => {
    console.error("Events WebSocket error observed:", error);
  };

  eventsSocket.onclose = (event) => {
    if (eventsSocketClosed) return;
    console.log("Events WebSocket is closed. Reconnect will be attempted in 1 second.", event.reason);
    setTimeout(() => {
      if (!eventsSocketClosed && currentFlowIdForSockets) {
        connectEventsWebSocketForFlow(currentFlowIdForSockets); // Reconnect, no initialFlowPromise needed
      }
    }, 1000);
  };
};

const getAndSubscribeSubflowWithDebounce = (subflowId: string, delay: number) => {
  // Clear existing timer for this subflowId, if any
  if (subflowProcessingDebounceTimers.value[subflowId]) {
    clearTimeout(subflowProcessingDebounceTimers.value[subflowId]);
  }

  subflowProcessingDebounceTimers.value[subflowId] = setTimeout(async () => {
    try {
      let subflowData = subflowsById.value[subflowId];

      if (!subflowData) {
        const response = await fetch(`/api/v1/workspaces/${store.workspaceId}/subflows/${subflowId}`);
        if (response.ok) {
          subflowData = (await response.json()).subflow as Subflow;
          subflowsById.value[subflowId] = subflowData;
        } else {
          console.error(`Failed to fetch subflow ${subflowId}: ${response.status}`, await response.text());
          return
        }
      }

      // Ensure subflowData is available before proceeding (either from cache or successful fetch)
      if (!subflowData) {
          console.error(`Subflow data for ${subflowId} is still not available after cache check and fetch attempt.`);
          return
      }
      
      // Send subscription message for the current subflowId
      eventsSocket?.send(JSON.stringify({ parentId: subflowId }));

      // If there's a parent subflow, process it with a 10ms debounce
      if (subflowData.parentSubflowId) {
        getAndSubscribeSubflowWithDebounce(subflowData.parentSubflowId, 10);
      }
    } catch (err) {
      console.error(`Error processing subflow ${subflowId}:`, err);
    } finally {
      delete subflowProcessingDebounceTimers.value[subflowId];
    }
  }, delay);
};

const connectActionChangesWebSocketForFlow = (flowId: string) => {
  actionChangesSocket = new WebSocket(`ws://${window.location.host}/ws/v1/workspaces/${store.workspaceId}/flows/${flowId}/action_changes_ws`);

  actionChangesSocket.onopen = () => {
    console.log("WebSocket connection opened for flow:", flowId);
  };

  actionChangesSocket.onmessage = async (event) => {
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
              flow.value = flowData.flow;
            }
          } catch (err) {
            console.error(`Error reloading flow data:`, err);
          }
        }
      }

      // ensure we get subflow
      if (flowAction.subflowId) {
        getAndSubscribeSubflowWithDebounce(flowAction.subflowId, 100);
      }

      // Existing logic for flowAction processing
      clearTimeout(subscribeStreamDebounceTimers[flowAction.id]);
      subscribeStreamDebounceTimers[flowAction.id] = setTimeout(() => {
        let latestFlowAction = flowActions.value.find((action) => action.id === flowAction.id);
        if (latestFlowAction?.actionStatus === 'started') {
          // Subscribe to events for the flow action itself (e.g., for chat messages)
          eventsSocket?.send(JSON.stringify({ parentId: flowAction.id }));
        }
        setShortContent();
      }, 100);

      const index = flowActions.value.findIndex((action) => action.id === flowAction.id);
      if (index !== -1) {
        if (flowAction.updated < flowActions.value[index].updated) {
          // events may be out of order at this point, even though the backend
          // provides them in order, if we do any awaits in the event handlers
          return;
        }
        flowActions.value[index] = flowAction;
      } else {
        flowActions.value.push(flowAction);
      }
      
      clearTimeout(subflowTreeDebounceTimer);
      subflowTreeDebounceTimer = setTimeout(() => {
        updateSubflowTrees();
      }, 100);
    } catch (err) {
      console.error("Error parsing WebSocket message for action changes", err);
    }
  };

  actionChangesSocket.onerror = (error) => {
    console.error("ActionChanges WebSocket error observed:", error);
  };

  actionChangesSocket.onclose = (event) => {
    if (actionChangesSocketClosed) return;
    console.log("ActionChanges WebSocket is closed. Reconnect will be attempted in 1 second.", event.reason);
    setTimeout(() => {
      if (!actionChangesSocketClosed && currentFlowIdForSockets) {
        connectActionChangesWebSocketForFlow(currentFlowIdForSockets);
      }
    }, 1000);
  };
};

const setupFlow = async (newFlowId: string | undefined) => {
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
  isLoadingFlow.value = true;
  isStartingFlow.value = false;
  hasReceivedFirstAction = false;

  // Reset states for the new flow
  flow.value = null;
  flowActions.value = [];
  activeDevStep.value.clear();
  subflowsById.value = {};
  // Clear any pending subflow status update timers
  Object.keys(subflowStatusUpdateDebounceTimers).forEach(key => {
    clearTimeout(subflowStatusUpdateDebounceTimers[key]);
  });
  subflowStatusUpdateDebounceTimers = {};

  // Clear any pending subflow processing timers
  Object.values(subflowProcessingDebounceTimers.value).forEach(timerId => clearTimeout(timerId));
  subflowProcessingDebounceTimers.value = {};

  updateSubflowTrees(); // Clear trees

  const flowPromise = fetch(`/api/v1/workspaces/${store.workspaceId}/flows/${newFlowId}`);

  connectEventsWebSocketForFlow(newFlowId, flowPromise);
  connectActionChangesWebSocketForFlow(newFlowId);

  try {
    const response = await flowPromise;
    if (response.ok) {
      const flowData = await response.json();
      flow.value = flowData.flow;
      isLoadingFlow.value = false;
      isStartingFlow.value = true;
    } else {
      console.error(`Failed to fetch flow ${newFlowId}:`, await response.text());
      flow.value = null;
      isLoadingFlow.value = false;
      isStartingFlow.value = false;
    }
  } catch (err) {
    console.error(`Error fetching flow ${newFlowId}:`, err);
    flow.value = null;
    isLoadingFlow.value = false;
    isStartingFlow.value = false;
  }
  setShortContent();
};


onMounted(async () => {
  const initialFlowId = route.params.id as string;
  if (initialFlowId) {
    await setupFlow(initialFlowId);
  }
  
  useEventBus('flow-view-collapse').on(() => {
    shortContent.value = true;
  });
});

watch(() => route.params.id, async (newFlowId, oldFlowId) => {
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

let setShortContent = () => {
  setTimeout(() => {
    const contentHeight = document.querySelector('.scroll-container')?.scrollHeight || 0
    const containerHeight = document.querySelector('.flow-actions-container')?.clientHeight || 0
    shortContent.value = contentHeight <= containerHeight
  }, 10)
}

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
  actionChangesSocketClosed = true; // Prevent reconnects
  if (actionChangesSocket) {
    actionChangesSocket.close();
  }
  eventsSocketClosed = true; // Prevent reconnects
  if (eventsSocket) {
    eventsSocket.close();
  }
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

.editor-links {
  position: absolute;
  z-index: 1000;
  top: 1rem;
  right: 1rem;
}

.editor-links a > * {
  height: 1.2rem;
  vertical-align: middle;
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