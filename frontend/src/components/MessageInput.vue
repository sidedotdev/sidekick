<template>
  <div class="message-input">
    <textarea v-model="content"></textarea>
    <button @click="sendMessage">Send</button>
  </div>
</template>

<script lang="ts">
import { defineComponent, ref } from 'vue'

export default defineComponent({
  emits: ['send'],

  setup(_, { emit }) {
    const content = ref<string>('')

    function sendMessage(): void {
      if (content.value.trim()) {
        emit('send', content.value.trim())
        content.value = ''
      }
    }

    return {
      content,
      sendMessage,
    }
  },
})
</script>

<style scoped>
.message-input {
  display: flex;
  gap: 10px;
}
textarea {
  flex: 1;
  height: 14vh;
}
</style>
