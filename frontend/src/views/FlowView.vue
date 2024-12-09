<template>
  <div class="flow-actions-container" :class="{ 'short-content': shortContent }">
    <div class="scroll-container">
      <SubflowContainer v-for="(subflowTree, index) in subflowTrees" :key="index" :subflowTree="subflowTree" :defaultExpanded="index == subflowTrees.length - 1"/>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref, onUnmounted } from 'vue'
import { useEventBus } from '@vueuse/core'
import SubflowContainer from '@/components/SubflowContainer.vue'
import type { FlowAction, SubflowTree, ChatMessageDelta } from '../lib/models'
import { buildSubflowTrees } from '../lib/subflow'
import { useRoute } from 'vue-router'
import { store } from '../lib/store'

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

let actionChangesSocket: WebSocket | null = null
let actionChangesSocketClosed = false
let eventsSocket: WebSocket | null = null
let eventsSocketClosed = false;
let shortContent = ref(true);

let setShortContent = () => {
  setTimeout(() => {
    const contentHeight = document.querySelector('.scroll-container')?.scrollHeight || 0
    const containerHeight = document.querySelector('.flow-actions-container')?.clientHeight || 0
    shortContent.value = contentHeight <= containerHeight
  }, 10)
}

onMounted(async () => {
  setShortContent()
  useEventBus('flow-view-collapse').on(() => {
    shortContent.value = true
  })

  // Connect to the new WebSocket for flow events. Note this will replace the flow action changes WebSocket eventually
  const connectEventsWebSocket = () => {
    eventsSocket = new WebSocket(`ws://${window.location.host}/ws/v1/workspaces/${store.workspaceId}/flows/${route.params.id}/events`);

    eventsSocket.onopen = () => {
      console.log("Events WebSocket connection opened");
    };

    eventsSocket.onmessage = (event) => {
      try {
        setShortContent()
        const flowEvent = JSON.parse(event.data);
        if (flowEvent.eventType === 'chat_message_delta') {
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
})

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
</style>