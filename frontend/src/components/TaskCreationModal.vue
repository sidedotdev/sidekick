<template>
  <div class="overlay" @click="safeClose"></div>
  <div class="modal">
    <h2>Create a New Task</h2>
    <form @submit.prevent="submitTask">
      <label for="status">Status</label>
      <select id="status" v-model="status" required>
        <option value="to_do">TODO</option>
        <option value="drafting">Drafting</option>
      </select>
      <br>
      <template v-if="status !== 'drafting'">
        <label for="flowType">Flow</label>
        <select id="flowType" v-model="flowType" required>
          <option value="basic_dev">Basic Dev</option>
          <option value="planned_dev">Planned Dev</option>
          <!--option>PM + Planned Dev</option-->
        </select>
        <div>
          <input type="checkbox" id="determineRequirements" v-model="determineRequirements">
          &nbsp;
          <label for="determineRequirements">Determine Requirements</label>
        </div>
        <div>
          <label>Environment Type</label>
          <SegmentedControl
            v-model="envType"
            :options="[
              { label: 'Repo Directory', value: 'local' },
              { label: 'Git Worktree', value: 'local_git_worktree' }
            ]"
          />
        </div>
      </template>
      <AutogrowTextarea id="description" v-model="description" placeholder="Describe your task in detail" required></AutogrowTextarea>
      <template v-if="false && flowType === 'planned_dev' && status === 'to_do'">
        <AutogrowTextarea id="planningPrompt" v-model="planningPrompt" placeholder="Planning prompt (optional)"></AutogrowTextarea>
        <br>
      </template>
      <a @click="$emit('close')" class="close">Cancel</a>
      <a @click="submitTask" class="cta-button" style="float: right;">Create Task</a>
    </form>
  </div>
</template>
<script setup lang="ts">
import { ref } from 'vue'
import AutogrowTextarea from './AutogrowTextarea.vue'
import SegmentedControl from './SegmentedControl.vue'
import { store } from '../lib/store'
const emit = defineEmits(['created', 'close'])
const props = defineProps({
  status: {
    type: String,
    default: 'to_do'
  }
})
const description = ref('')
const status = ref(props.status || 'to_do')
const planningPrompt = ref('')
const determineRequirements = ref(true)
const flowType = ref(localStorage.getItem('lastUsedFlowType') ?? 'basic_dev')
const envType = ref('local')
const submitTask = async () => {
  const flowOptions = {
    planningPrompt: planningPrompt.value,
    determineRequirements: determineRequirements.value,
    envType: envType.value
  }
  const response = await fetch(`/api/v1/workspaces/${store.workspaceId}/tasks`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ description: description.value, flowType: flowType.value, status: status.value, flowOptions }),
  })
  if (!response.ok) {
    console.error('Failed to create task')
    return
  }
  localStorage.setItem('lastUsedFlowType', flowType.value)
  description.value = ''
  flowType.value = ''
  status.value = 'to_do'
  planningPrompt.value = ''
  envType.value = 'local'
  emit('created')
  emit('close')
}
const safeClose =() => {
  if (description.value) {
    if (!window.confirm('Are you sure you want to close this modal? Your changes will be lost.')) {
      return
    }
  }
  emit('close')
}
</script>

<style scoped>
.overlay {
  position: fixed;
  top: 0;
  right: 0;
  bottom: 0;
  left: 0;
  background: rgba(0, 0, 0, 0.7);
  z-index: 100000;
}

.modal {
  font-family: sans-serif;
  border: 1px solid #454545;
  border-radius: 5px;
  justify-content: center;
  /*align-items: center;*/
  background-color: var(--color-modal-background);
  color: var(--color-modal-text);
  z-index: 100000 !important;
  padding: 30px;
  width: 60vw;
  position: fixed;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  overflow: auto;
  max-height: 100%;
  transition: background-color 0.5s, color 0.5s;
}

h2 {
  margin-bottom: 20px;
}

label {
  margin: 12px 0;
  min-width: 100px;
  display: inline-block
}

select {
  padding: 0.25rem;
  font-size: 1rem;
  background-color: var(--color-select-background);
  color: var(--color-select-text);
  border: 1px solid var(--color-select-border);
  border-radius: 0.25rem;
  min-width: 150px;
}

form {
  width: 100%;
}

#description {
  width: 100%;
  min-height: 100px;
  font-size: 16px;
  margin: 10px 0;
}

.close {
  font-size: 16px;
  color: var(--color-modal-text);
  font-family: "JetBrains Mono", monospace;
  display: inline-block;
  margin-top: 6px;
}
</style>