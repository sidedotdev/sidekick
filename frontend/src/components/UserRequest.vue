<template>
  <form v-if="isPending" @submit.prevent="flowAction.actionParams.requestKind === 'free_form' && submitUserResponse(true)">
    <p>{{ flowAction.actionParams.requestContent }}</p>
    <AutogrowTextarea v-model="responseContent" :placeholder="flowAction.actionParams.requestKind === 'approval' ? 'Content is only required for the revise option' : ''" />
    <div v-if="flowAction.actionParams.requestKind === 'approval'">
      <button type="button" class="cta-button-color"
        :disabled="responseContent.trim() !== ''"
        @click="submitUserResponse(true)"
      >
        {{ approveCopy() }}
      </button>

      <button type="button" class="secondary"
        :disabled="responseContent.trim() === ''"
        @click="submitUserResponse(false)"
      >
        {{ rejectCopy() }}
      </button>
    </div>
    <div v-else>
      <button :disabled="responseContent.length == 0" class="cta-button-color" type="submit">Submit</button>
    </div>
  </form>
  <!-- TODO move approved/rejected to FlowActionItem summary. Only show content here -->
  <div v-if="expand && !isPending">
    <div v-if="flowAction.actionParams.requestContent">
      <p><i>{{ flowAction.actionParams.requestContent }}</i></p>
    </div>
    <div v-if="flowAction.actionParams.requestKind == 'approval'">
      <!--p>{{ flowAction.actionParams.requestContent }}</p-->
      <p>{{ parsedActionResult.Approved ? '✅ Approved' : '❌ Rejected: ' }}{{ parsedActionResult.Content }}</p>
    </div>
    <div class="free-form" v-else-if="flowAction.actionParams.requestKind == 'free_form'">
      <p v-if="parsedActionResult.Content"><b>You: </b>{{ parsedActionResult.Content }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue';
import type { FlowAction } from '../lib/models';
import AutogrowTextarea from './AutogrowTextarea.vue';

interface UserResponse {
  content?: string;
  approved?: boolean;
  choice?: string;
}

const props = defineProps({
  flowAction: {
    type: Object as () => FlowAction,
    required: true,
  },
  expand: {
    type: Boolean,
    required: true,
  },
});

const responseContent = ref('');
const isPending = computed(() => props.flowAction.actionStatus === 'pending');

const parsedActionResult = computed(() => {
  try {
    return JSON.parse(props.flowAction.actionResult);
  } catch (error) {
    return null;
  }
});

// temporary until we set up i18n
const tags: {[key: string]: string} = {
  "approve_plan": "Approve",
  "reject_plan": "Revise",
}

function rejectCopy(): string {
  const tag: string | undefined = props.flowAction.actionParams.rejectTag
  return tag && tags[tag] ? tags[tag] : "Reject"
}

function approveCopy(): string {
  const tag: string | undefined = props.flowAction.actionParams.approveTag
  return tag && tags[tag] ? tags[tag] : "Approve"
}

async function submitUserResponse(approved: boolean) {
  if (props.flowAction.actionStatus !== 'pending') {
    return;
  }

  const userResponse: UserResponse = {
    content: responseContent.value,
  };

  if (props.flowAction.actionParams.requestKind === 'approval') {
    userResponse.approved = approved;
  }

  try {
    const response: Response = await fetch(`/api/v1/workspaces/${props.flowAction.workspaceId}/flow_actions/${props.flowAction.id}/complete`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        userResponse: userResponse,
      }),
    })
    // TODO /gen show the error in the UI
    if (!response.ok) {
      console.error('Failed to complete flow action')
    }
    console.debug(await response.json())
    return false
  } catch (error) {
    // TODO /gen show the error in the UI
    console.error(error);
  }
}
</script>

<style scoped>
form {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

p {
  white-space: pre-wrap;
}

button {
  padding: 0.5rem 1rem;
  border: none;
  cursor: pointer;
  margin-right: 0.5rem;
  text-shadow: 1px 1px 1px rgba(0, 0, 0, 0.3);
  border-radius: 4px;
  color: white;
}

button.secondary {
  background-color: #5b636a;
}

button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}
button:disabled:hover {
  filter: none;
}

textarea {
  width: 100%;
  padding: 0.5rem;
  border: 1px solid #ccc;
  resize: none;
}

.free-form p + p {
  margin-top: 5px;
}

b {
  font-weight: bold;
}
</style>