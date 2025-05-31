<template>
  <div v-if="flow">
    <div class="editor-links" v-if="devMode">
      <p v-for="worktree in flow.worktrees" :key="worktree.id">
        Open Worktree:
        <a :href="`vscode://file/${workDir(worktree)}`">VS Code</a>
        |
        <a :href="`idea://open?file=${encodeURIComponent(workDir(worktree))}`" class="vs-code-button">Intellij IDEA</a>
      </p>
    </div>
    <div 
      v-if="flow && !['completed', 'failed', 'canceled', 'paused'].includes(flow.status)" 
      class="flow-controls-container"
    >
      <button @click="pauseFlow" class="pause-button">⏸︎</button>
      <button 
        v-if="isActiveFollowDevPlanSubflow"
        @click="triggerNextStep" 
        class="next-button"
      >
        Next Step
      </button>
    </div>
  </div>
  <div class="flow-actions-container" :class="{ 'short-content': shortContent }">
    <div class="scroll-container">
      <SubflowContainer v-for="(subflowTree, index) in subflowTrees" :key="index" :subflowTree="subflowTree" :defaultExpanded="index == subflowTrees.length - 1"/>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, onUnmounted, watch } from 'vue'
import { useEventBus } from '@vueuse/core'
import SubflowContainer from '@/components/SubflowContainer.vue'
import type { FlowAction, SubflowTree, ChatMessageDelta, Flow, Worktree, Subflow } from '../lib/models'
import { SubflowStatus } from '../lib/models'
import { buildSubflowTrees } from '../lib/subflow'
import { useRoute } from 'vue-router'
import { store } from '../lib/store'

const dataDir = `${import.meta.env.VITE_HOME}/Library/Application Support/Sidekick` // FIXME switch to API call to backend
const devMode = import.meta.env.MODE === 'development'
const flowActions = ref<FlowAction[]>([])
const subflowTrees = ref<SubflowTree[]>([])
const route = useRoute()

const updateSubflowTrees = () => {
  // we only want to show pending user requests, since we use that as the way to
  // show the form for a request. complete ones are shown under the "Tool:
  // get_help_or_input" action
  const relevantFlowActions = flowActions.value
  /*
  .filter((action) => {
    return action.actionType !== 'user_request' || action.actionStatus !== 'complete'
  })
    */
  const newSubtrees = buildSubflowTrees(relevantFlowActions)
  subflowTrees.value = newSubtrees
}

let flow = ref<Flow | null>(null)
let actionChangesSocket: WebSocket | null = null
let actionChangesSocketClosed = false
let eventsSocket: WebSocket | null = null
let eventsSocketClosed = false;
let shortContent = ref(true);

const isActiveFollowDevPlanSubflow = ref(false);

const updateActiveFollowDevPlanStatus = async () => {
  if (!flow.value || !flowActions.value || flowActions.value.length === 0) {
    isActiveFollowDevPlanSubflow.value = false;
    return;
  }

  for (const action of flowActions.value) {
    if (action.actionType === 'follow_dev_plan' && action.subflowId) {
      try {
        const response = await fetch(`/api/v1/workspaces/${store.workspaceId}/subflows/${action.subflowId}`);
        if (response.ok) {
          const subflow = await response.json() as Subflow;
          if (subflow.status === SubflowStatus.Started) {
            isActiveFollowDevPlanSubflow.value = true;
            return; // Found an active one, no need to check further
          }
        } else {
          console.warn(`Failed to fetch subflow ${action.subflowId}: ${response.status}`);
        }
      } catch (err) {
        console.error(`Error fetching subflow ${action.subflowId}:`, err);
      }
    }
  }
  isActiveFollowDevPlanSubflow.value = false; // No active follow_dev_plan subflow found
};

watch([() => flow.value?.id, flowActions], updateActiveFollowDevPlanStatus, { immediate: true, deep: true });

const triggerNextStep = async () => {
  if (!flow.value) return;
  try {
    const response = await fetch(`/api/v1/workspaces/${store.workspaceId}/flows/${flow.value.id}/user_action`, {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ action_type: 'go_next_step' }),
    });
    if (!response.ok) {
      const errorBody = await response.text();
      console.error(`Failed to trigger next step: ${response.status}`, errorBody);
      // TODO: Optionally, provide user feedback here e.g. via a toast notification
    } else {
      console.log('Next step triggered successfully');
    }
  } catch (err) {
    console.error('Error triggering next step:', err);
    // TODO: Optionally, provide user feedback here
  }
};

let setShortContent = () => {
  setTimeout(() => {
    const contentHeight = document.querySelector('.scroll-container')?.scrollHeight || 0
    const containerHeight = document.querySelector('.flow-actions-container')?.clientHeight || 0
    shortContent.value = contentHeight <= containerHeight
  }, 10)
}

onMounted(async () => {
  const flowPromise = fetch(`/api/v1/workspaces/${store.workspaceId}/flows/${route.params.id}`)
  setShortContent()
  useEventBus('flow-view-collapse').on(() => {
    shortContent.value = true
  })

  // Connect to the new WebSocket for flow events. Note this will replace the flow action changes WebSocket eventually
  const connectEventsWebSocket = () => {
    eventsSocket = new WebSocket(`ws://${window.location.host}/ws/v1/workspaces/${store.workspaceId}/flows/${route.params.id}/events`);

    eventsSocket.onopen = async () => {
      console.log("Events WebSocket connection opened");
      await flowPromise
      setTimeout(() => {
        const message = JSON.stringify({parentId: flow.value?.id});
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

              if (delta.content) {
                contentBuilder.push(delta.content);
              }

              if (delta.toolCalls) {
                delta.toolCalls.forEach(toolCall => {
                  if (toolCall.name) {
                    contentBuilder.push(`toolName = ${toolCall.name}\n`);
                  }
                  if (toolCall.arguments) {
                    contentBuilder.push(toolCall.arguments);
                  }
                });
              }

              action.actionResult += contentBuilder.join('\n');
              flowActions.value[actionIndex] = action;
            } else {
              console.error(`FlowAction with id ${flowEvent.flowActionId} not found.`);
            }
          }
          break
          case 'status_change': {
            if (flow.value && flowEvent.parentId == flow.value.id) {
              flow.value.status = flowEvent.status;
            }
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
      if (eventsSocketClosed) {
        return;
      }
      console.log("Events WebSocket is closed. Reconnect will be attempted in 1 second.", event.reason);
      setTimeout(() => {
        connectEventsWebSocket();
      }, 1000);
    };
  };
  connectEventsWebSocket();

  const connectActionChangesWebSocket = () => {
    console.log("connectWebSocket")
    actionChangesSocket = new WebSocket(`ws://${window.location.host}/ws/v1/workspaces/${store.workspaceId}/flows/${route.params.id}/action_changes_ws`);

    actionChangesSocket.onopen = () => {
      console.log("WebSocket connection opened");
    };

    let subflowTreeDebounceTimer: NodeJS.Timeout;
    let subscribeStreamDebounceTimers: {[key: string]: NodeJS.Timeout} = {};
    actionChangesSocket.onmessage = (event) => {
      try {
        const flowAction: FlowAction = JSON.parse(event.data);
        const index = flowActions.value.findIndex((action) => action.id === flowAction.id);

        // get events for this flow action, if status is still "started" after
        // waiting 100ms (we wait in case a followup action change already
        // happened but hasn't streamed in yet, telling us it's already
        // completed)
        clearTimeout(subscribeStreamDebounceTimers[flowAction.id]);
        subscribeStreamDebounceTimers[flowAction.id] = setTimeout(() => {
          let latestFlowAction = flowActions.value.find((action) => action.id === flowAction.id);
          // started means it's in progress
          if (latestFlowAction?.actionStatus === 'started') {
            const message = JSON.stringify({parentId: flowAction.id});
            eventsSocket?.send(message);
          }
          setShortContent()
        }, 100)

        if (index !== -1) {
          flowActions.value[index] = flowAction;
        } else {
          flowActions.value.push(flowAction);
        }
        // Debounce this call for UI updates
        clearTimeout(subflowTreeDebounceTimer);
        subflowTreeDebounceTimer = setTimeout(() => {
          updateSubflowTrees();
        }, 100);
      } catch (err) {
        console.error("Error parsing WebSocket message", err);
      }
    };

    actionChangesSocket.onerror = (error) => {
      console.error("WebSocket error observed:", error);
      // You might want to attempt reconnection here
    };

    actionChangesSocket.onclose = (event) => {
      if (actionChangesSocketClosed) {
        return;
      }
      console.log("WebSocket is closed. Reconnect will be attempted in 1 second.", event.reason);
      setTimeout(() => {
        connectActionChangesWebSocket();
      }, 1000);
    };
  };

  connectActionChangesWebSocket();

  const response = await flowPromise
  flow.value = (await response.json()).flow
})

const workDir = (worktree: Worktree): string => {
  return `${dataDir}/worktrees/${worktree.workspaceId}/${worktree.name}`
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
  if (actionChangesSocket !== null) {
    actionChangesSocketClosed = true
    actionChangesSocket.close()
  }
  if (eventsSocket !== null) {
    eventsSocketClosed = true
    eventsSocket.close()
  }
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
</style>