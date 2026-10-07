<template>
  <div v-if="failed" role="alert">
    Unable to display this action ({{ actionId }}). Other actions will continue updating.
  </div>
  <slot v-else />
</template>

<script setup lang="ts">
import { onErrorCaptured, ref } from 'vue'

const props = defineProps<{ actionId: string }>()
const failed = ref(false)

onErrorCaptured((error, _instance, info) => {
  failed.value = true
  console.error(`Error displaying flow action ${props.actionId} (${info}):`, error)
  return false
})
</script>