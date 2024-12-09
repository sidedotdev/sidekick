<template>
  <div class="chat-history">
    <div class="scroll-container">
      <div v-for="message in messages" :key="message.id" class="message">
        <div class="profile-thumb" :class="{ user: message.role == 'user' }"></div>
        <div>
          <strong>{{ message.role }}</strong>
          <div v-for="event in message.events" :key="event" class="message">
            {{ event }}
          </div>
          <pre>{{ message.content }}</pre>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { Message } from '../lib/models.ts'

defineProps<{
  messages: Message[]
}>()
</script>

<style scoped>
.chat-history {
  width: 100%;
  height: 70vh;
  border: 1px solid #eee;
  padding: 10px;
  margin-bottom: 20px;

  overflow: auto;
  display: flex;
  flex-direction: column-reverse;
}

.profile-thumb {
  height: 40px;
  width: 40px;
  background-color: #9e8;
  border-radius: 10%;
  flex-shrink: 0;
  margin-right: 10px;
}
.profile-thumb.user {
  background-color: #555;
}

.message {
  margin-top: 5px;
  padding-top: 5px;
  padding-bottom: 5px;
  display: flex;
}

strong {
  font-weight: bold;
  text-transform: capitalize;
  line-height: 1;
  display: block;
  margin-bottom: 5px;
}
.scroll-container {
  /* Tried to make the text stay at the top when it's short, but broke scrolling it to the bottom when it's long */
  /*
  min-height: 100%;
  overflow-y: scroll;
  */
}

pre {
  white-space: pre-wrap;
}
</style>
