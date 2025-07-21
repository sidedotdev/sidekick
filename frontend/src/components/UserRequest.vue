<template>
  <form v-if="isPending" @submit.prevent="flowAction.actionParams.requestKind === 'free_form' && submitUserResponse(true)">
    <div v-if="flowAction.actionParams.requestContent" class="markdown-container">
      <button v-if="flowAction.actionParams.requestKind === 'approval'" class="copy-button" @click.stop="copyRequestContent" title="Copy request content">
        <CopyIcon />
      </button>
      <vue-markdown
        :source="flowAction.actionParams.requestContent"
        :options="{ breaks: true }"
        class="markdown"
      />
    </div>
    <div v-if="flowAction.actionParams.command">
      <pre>{{ flowAction.actionParams.command }}</pre>
    </div>
    <template v-if="flowAction.actionParams.mergeApprovalInfo?.diff">
      <UnifiedDiffViewer
        :diff-string="flowAction.actionParams.mergeApprovalInfo.diff"
        :default-expanded="false"
      />
    </template>

    <div v-if="flowAction.actionParams.requestKind === 'approval'">
      <AutogrowTextarea v-model="responseContent" placeholder="Rejection reason" />
      <div v-if="errorMessage" class="error-message">
        {{ errorMessage }}
      </div>
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

    <div v-else-if="flowAction.actionParams.requestKind === 'merge_approval'">
      <div style="display: flex; margin-top: 0.5rem;">
        <label for="targetBranch">Merge into</label>
        <BranchSelector
          id="targetBranch"
          v-model="targetBranch"
          :workspaceId="flowAction.workspaceId"
        />
      </div>

      <AutogrowTextarea v-model="responseContent" placeholder="Rejection reason" />
      <div v-if="errorMessage" class="error-message">
        {{ errorMessage }}
      </div>
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
    <div v-else-if="flowAction.actionParams.requestKind === 'continue'">
      <button type="button" class="cta-button-color"
        :disabled="!isPending"
        @click="submitUserResponse(true)"
      >
        {{ continueCopy() }}
      </button>
    </div>
    <div v-else>
      <AutogrowTextarea v-model="responseContent"/>
      <div v-if="errorMessage" class="error-message">
        {{ errorMessage }}
      </div>
      <button :disabled="responseContent.length == 0" class="cta-button-color" type="submit">Submit</button>
    </div>
  </form>
  <!-- TODO move approved/rejected to FlowActionItem summary. Only show content here -->
  <div v-if="expand && !isPending && (!flowAction.actionParams.command || !parsedActionResult.Approved)">
    <div v-if="flowAction.actionParams.requestContent" class="markdown-container">
      <button v-if="flowAction.actionParams.requestKind === 'approval'" class="copy-button" @click.stop="copyRequestContent" title="Copy request content">
        <CopyIcon />
      </button>
      <vue-markdown
        :source="flowAction.actionParams.requestContent"
        :options="{ breaks: true }"
        class="markdown"
      />
    </div>
    <div v-if="flowAction.actionParams.command">
      <pre>{{ flowAction.actionParams.command }}</pre>
    </div>
    <template v-if="flowAction.actionParams.mergeApprovalInfo?.diff">
      <UnifiedDiffViewer
        :diff-string="flowAction.actionParams.mergeApprovalInfo.diff"
        :default-expanded="false"
      />
    </template>
    <div v-if="parsedActionResult?.Params?.targetBranch">
      Merge into: {{ parsedActionResult.Params.targetBranch }}
    </div>
    <div v-if="/approval/.test(props.flowAction.actionParams.requestKind)">
      <!--p>{{ flowAction.actionParams.requestContent }}</p-->
      <p>{{ parsedActionResult.Approved ? '✅ Approved' : '❌ Rejected: ' }}{{ parsedActionResult.Content }}</p>
    </div>
    <div class="free-form" v-else-if="flowAction.actionParams.requestKind == 'free_form'">
      <p v-if="parsedActionResult.Content"><b>You: </b>{{ parsedActionResult.Content }}</p>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue';
import type { FlowAction } from '../lib/models';
import AutogrowTextarea from './AutogrowTextarea.vue';
import BranchSelector from './BranchSelector.vue'
import VueMarkdown from 'vue-markdown-render'
import UnifiedDiffViewer from './UnifiedDiffViewer.vue';
import CopyIcon from './icons/CopyIcon.vue';

interface UserResponse {
  content?: string;
  approved?: boolean;
  choice?: string;
  params?: { [key: string]: any };
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
  level: {
    type: Number,
    default: 0,
  },
});

const responseContent = ref('');
const errorMessage = ref('');
const isPending = computed(() => props.flowAction.actionStatus === 'pending');

function getStorageKey(actionId: string): string {
  return `sidekick_user_request_draft_${actionId}`;
}

function clearDraft() {
  try {
    localStorage.removeItem(getStorageKey(props.flowAction.id));
  } catch (error) {
    console.debug('Failed to clear draft:', error);
  }
}

watch(responseContent, (newContent) => {
  try {
    localStorage.setItem(getStorageKey(props.flowAction.id), newContent);
  } catch (error) {
    console.debug('Failed to save draft:', error);
  }
});

onMounted(() => {
  try {
    const savedContent = localStorage.getItem(getStorageKey(props.flowAction.id));
    if (savedContent) {
      responseContent.value = savedContent;
    }
  } catch (error) {
    console.debug('Failed to load draft:', error);
  }
});

const parsedActionResult = computed(() => {
  try {
    return JSON.parse(props.flowAction.actionResult);
  } catch (error) {
    return null;
  }
});

const targetBranch = ref<string | undefined>(parsedActionResult.value?.targetBranch ?? props.flowAction.actionParams.mergeApprovalInfo?.defaultTargetBranch)

// temporary until we set up i18n
const tags: {[key: string]: string} = {
  "approve_plan": "Approve",
  "reject_plan": "Revise",
  "done": "Done",
  "try_again": "Try Again",
}

function rejectCopy(): string {
  const tag: string | undefined = props.flowAction.actionParams.rejectTag
  return tag && tags[tag] ? tags[tag] : "Reject"
}

function approveCopy(): string {
  const tag: string | undefined = props.flowAction.actionParams.approveTag
  return tag && tags[tag] ? tags[tag] : "Approve"
}

function continueCopy(): string {
  const tag: string | undefined = props.flowAction.actionParams.continueTag
  return tag && tags[tag] ? tags[tag] : "Continue"
}

const copyRequestContent = async () => {
  try {
    await navigator.clipboard.writeText(props.flowAction.actionParams.requestContent)
  } catch (err) {
    console.error('Failed to copy request content:', err)
  }
}

async function submitUserResponse(approved: boolean) {
  if (props.flowAction.actionStatus !== 'pending') {
    return;
  }

  // Clear any existing error message
  errorMessage.value = '';

  const userResponse: UserResponse = {
    content: responseContent.value,
  };

  if (props.flowAction.actionParams.requestKind === 'merge_approval') {
    userResponse.params = {
      targetBranch: targetBranch.value,
    };
  }

  if (/approval/.test(props.flowAction.actionParams.requestKind)){
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
    
    if (!response.ok) {
      try {
        const errorData = await response.json();
        errorMessage.value = errorData.error || 'Failed to complete flow action';
      } catch (parseError) {
        errorMessage.value = 'Failed to complete flow action';
      }
      return false;
    }
    
    // Success case - parse response once
    const result = await response.json();
    console.debug(result);
    clearDraft();
    return true;
  } catch (error) {
    errorMessage.value = 'Network error: Failed to submit response';
    console.error(error);
    return false;
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
  overflow-x: scroll;
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

:deep(textarea) {
  margin-top: 15px;
  margin-bottom: 15px;
}

.free-form p + p {
  margin-top: 5px;
}

b {
  font-weight: bold;
}

label[for="targetBranch"] {
  align-self: center;
  margin-right: 1rem;
}

.markdown {
  max-width: 60rem;
}

.markdown :deep(h4) {
  font-size: 120%;
  margin: 2rem 0 1rem;
}

.markdown :deep(pre) {
  border: 2px solid var(--color-border-contrast);
  padding: 1rem;
  margin-bottom: 1rem;
}
.markdown :deep(strong) {
  font-weight: bold;
}

/* FIXME make this dependent on light vs dark theme */
.markdown :deep(li > code) {
  padding: 2px 4px;
  font-size: 90%;
  color: #6bc725;
  background-color: #000;
  border-radius: 4px;
}

.markdown :deep(li) {
  margin-top: 0.5rem;
}
.markdown :deep(p:not(:first-child)) {
  margin-top: 15px;
}
.markdown :deep(ol), .markdown :deep(ul) {
  margin-top: 1rem;
}

.markdown :deep(li > ul), .markdown :deep(li > ol) {
  margin-top: 0.5rem;
}

.error-message {
  background-color: var(--color-error-bg, #fee);
  color: var(--color-error-text, #c33);
  border: 1px solid var(--color-error-border, #fcc);
  border-radius: 4px;
  padding: 0.75rem;
  margin: 0.5rem 0;
  font-size: 0.9rem;
}

.markdown-container {
  position: relative;
  --level: v-bind(level);
}

.copy-button {
  position: sticky;
  top: calc(var(--level) * var(--name-height)); /* --name-height is made available by FlowActionItem */
  right: 0.5rem;
  float: right;
  background: none;
  border: none;
  cursor: pointer;
  padding: 0.25rem;
  border-radius: 0.25rem;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: background-color 0.2s;
  z-index: calc(50 - var(--level));
  margin-top: 0.5rem;
  margin-bottom: -2rem;
}

.copy-button:hover {
  background: var(--color-background-hover);
}

.copy-button svg {
  width: 1rem;
  height: 1rem;
  fill: var(--color-text);
  stroke: var(--color-text);
}
</style>