<template>
  <KanbanBoard :tasks="tasks" @refresh="fetchTasks" />
</template>

<script setup lang="ts">
import { ref, onMounted, watch, onUnmounted } from 'vue'
import type { Ref } from 'vue'
import KanbanBoard from '../components/KanbanBoard.vue'
import { store } from '../lib/store'

const tasks: Ref<Array<any>> = ref([])
let socket: WebSocket | null = null
let socketClosed = false
let lastTaskStreamId: string | null = null

const fetchTasks = async () => {
  if (store.workspaceId) {
    try {
      const response = await fetch(`/api/v1/workspaces/${store.workspaceId}/tasks`)
      tasks.value = (await response.json()).tasks
    } catch (error) {
      console.error('Failed to fetch tasks:', error)
    }
  }
}

const connectWebSocket = (onConnect: (() => void)) => {
  console.log("connectWebSocket task changes");
  const streamIdParam = lastTaskStreamId ? `?lastTaskStreamId=${lastTaskStreamId}` : '';
  console.log({streamIdParam});
  socket = new WebSocket(
    `ws://${window.location.host}/ws/v1/workspaces/${store.workspaceId}/task_changes${streamIdParam}`
  );

  socket.onopen = () => {
    console.log("WebSocket connection opened");
    onConnect();
  };

  socket.onmessage = (event) => {
    const data = JSON.parse(event.data);
    lastTaskStreamId = data.lastTaskStreamId;  // Update the last task stream ID
    updateTasks(data.tasks);
  };

  socket.onerror = (error) => {
    console.error("WebSocket error observed:", error);
    // You might want to attempt reconnection here
  };

  socket.onclose = (event) => {
    console.log("WebSocket closed:", event);
    if (socketClosed) {
      return;
    }
    console.log(
      "WebSocket is closed. Reconnect will be attempted in 1 second.",
      event.reason
    );
    setTimeout(() => {
      connectWebSocket(fetchTasks);
    }, 1000);
  };
};

const updateTasks = (newTasks: Array<any>) => {
  // Merging or updating the current tasks based on new incoming tasks
  newTasks.forEach(task => {
    const index = tasks.value.findIndex(t => t.id === task.id);
    if (index !== -1) {
      tasks.value.splice(index, 1, task);
    } else {
      tasks.value.push(task);
    }
  });

  console.debug('Updated tasks:', tasks.value);
}

const initialize = async () => {
  connectWebSocket(fetchTasks);
}

const uninitialize = () => {
  console.log("uninitialize");
  if (socket !== null) {
    socketClosed = true
    socket.close()
    console.log("socket closed!");
  } else {
    console.log("socket is null");
  }
}

onMounted(initialize);
onUnmounted(uninitialize);

watch(() => store.workspaceId, () => {
  uninitialize()
  initialize()
})

</script>