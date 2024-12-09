<template>
  <div>
    <ChatHistory :messages="messages" />
    <MessageInput @send="submitMessage" />
  </div>
</template>

<script lang="ts" setup>
import { ref, onMounted } from 'vue'
import ChatHistory from '../components/ChatHistory.vue'
import MessageInput from '../components/MessageInput.vue'
import type { Message, ActionData } from '../lib/models.ts'
import { useRoute } from 'vue-router'
import { type CustomEventDataType, type CustomEventType, SSE, SSEOptionsMethod } from 'sse-ts'

const route = useRoute()
const topicId = route.params.id as string
const messages = ref<Message[]>([])
onMounted(fetchMessages)

async function fetchMessages(): Promise<void> {
  try {
    const response = await fetch(`/api/topics/${topicId}/messages`)
    messages.value = (await response.json()).messages as Message[]
    console.debug('Fetched messages:', messages.value)
  } catch (error) {
    console.error('Failed to fetch messages:', error)
  }
}

// helps test the UI, use it in place of submitMessage. simulates the backend's events without invoking it.
async function fakeSubmitMessage(content: string): Promise<void> {
  let state = { currentId: 'pending' }
  messages.value.push({ id: state.currentId, content, role: 'user' })
  const findMessage = (message?: Message) => {
    const existingMessageIndex = messages.value.findIndex((m) => m.id === state.currentId || m.id === message?.id)
    return {
      messagesValue: messages.value,
      index: existingMessageIndex,
      message: messages.value[existingMessageIndex],
    }
  }

  // simulates backend messages/create event
  setTimeout(() => {
    let createdMessage = { id: 'aowij', content, role: 'user' }
    let { messagesValue, index } = findMessage(createdMessage)
    if (index == -1) {
      // effectively appends if the message was not found
      index = messages.value.length
      state.currentId = createdMessage.id // required to ensure the findMessage() function returns the correct message for other events
    }
    messagesValue[index] = createdMessage
  }, 0)

  setTimeout(() => {
    let createdMessage = { id: 'another', content: '', role: 'assistant' }
    let { messagesValue, index } = findMessage(createdMessage)
    if (index == -1) {
      // effectively appends if the message was not found
      index = messages.value.length
      state.currentId = createdMessage.id // required to ensure the findMessage() function returns the correct message for other events
    }
    messagesValue[index] = createdMessage
  }, 1)

  // simulates backend inference thinking event
  setTimeout(() => {
    const { message } = findMessage()
    message.events = message.events || []
    message.events.push(`Thinking...`)
  }, 400)

  // simulates backend action event
  // TODO actual logic to overwrite event instead of hardcoding index
  setTimeout(() => {
    const { message } = findMessage()
    message.events = message.events || []
    message.events[0] = `Inferred Intent: new_development_request`
  }, 1500)

  // simulates backend new message created event
  setTimeout(() => {
    const { message } = findMessage()
    message.events = message.events || []
    message.events.push(`Started new dev workflow.`)
    message.content = ' ' + content
  }, 1700)

  // simulates backend messages/contentDelta event
  setTimeout(() => {
    const { message } = findMessage()
    message.events = message.events || []
    message.content = "It's happening!"
  }, 1800)

  // simulates backend messages/contentDelta event
  setTimeout(() => {
    const { message } = findMessage()
    message.content += " Here's some more text."
  }, 3000)
}

async function submitMessage(content: string): Promise<void> {
  try {
    // this setup supports reactive updates to the specific message with
    // only the top-level messages array being a ref
    let state = { currentId: 'pending' }
    messages.value.push({ id: state.currentId, content, role: 'user' })
    const findMessage = (message?: Message) => {
      const existingMessageIndex = messages.value.findIndex((m) => m.id === state.currentId || m.id === message?.id)
      return {
        messagesValue: messages.value,
        index: existingMessageIndex,
        message: messages.value[existingMessageIndex],
      }
    }

    const source = new SSE(`/api/topics/${topicId}/messages`, {
      method: SSEOptionsMethod.POST,
      payload: JSON.stringify({ content }),
    })

    source.addEventListener('message/create', (event: CustomEventType) => {
      console.log('create event', event)
      const dataEvent = event as CustomEventDataType
      const createdMessage = JSON.parse(dataEvent.data)
      let { messagesValue, index } = findMessage(createdMessage)
      if (index == -1) {
        // effectively appends if the message was not found
        index = messages.value.length
        state.currentId = createdMessage.id // required to ensure the findMessage() function returns the correct message for other events
      }
      messagesValue[index] = createdMessage
    })

    source.addEventListener('action', (event: CustomEventType) => {
      console.log('action event', event)
      const dataEvent = event as CustomEventDataType
      const { message } = findMessage()
      message.events = message.events || []
      const actionData = JSON.parse(dataEvent.data) as ActionData
      message.events.push(actionData.title)
    })

    source.addEventListener('message/error', (event: CustomEventType) => {
      console.log('error event', event)
      const dataEvent = event as CustomEventDataType
      const { message } = findMessage()
      message.events = message.events || []
      message.events.push(`Error: ${dataEvent.data}`)
    })

    source.addEventListener('message/contentDelta', (event: CustomEventType) => {
      console.log('contentDelta event', event)
      const dataEvent = event as CustomEventDataType
      const { message } = findMessage()
      message.content = message.content ?? ''
      message.content += JSON.parse(dataEvent.data) as string
    })

    source.stream()
  } catch (error) {
    console.error('Failed to send message:', error)
  }
}
</script>
