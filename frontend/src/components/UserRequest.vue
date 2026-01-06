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
      <div v-if="showEmptyDiffMessage" class="empty-diff-message">
        No changes since last review
      </div>
      <UnifiedDiffViewer
        v-else
        :diff-string="currentDiffString"
        :default-expanded="false"
        :diff-mode="diffMode"
        :level="level"
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
      <div  class="diff-options-row">
        <label for="diffScope">Show</label>
        <Select
          id="diffScope"
          v-model="diffScope"
          :options="diffScopeOptions"
          optionLabel="label"
          optionValue="value"
        ></Select>
        <DiffViewOptions
          v-model:ignoreWhitespace="ignoreWhitespace"
          v-model:diffMode="diffMode"
          :disabled="!isPending"
        />
      </div>
      <div style="display: flex; align-items: center; gap: 1rem; margin-top: 0.5rem;">
        <label for="targetBranch">Merge into</label>
        <BranchSelector
          id="targetBranch"
          v-model="targetBranch"
          :workspaceId="flowAction.workspaceId"
        />
      </div>
      <div style="display: flex; align-items: center; gap: 1rem; margin-top: 0.5rem;">
        <label for="mergeStrategy">Merge strategy</label>
        <Select
          id="mergeStrategy"
          v-model="mergeStrategy"
          :options="mergeStrategyOptions"
          optionLabel="label"
          optionValue="value"
        ></Select>
      </div>

      <DevRunControls
        v-if="flowAction.actionParams.mergeApprovalInfo?.devRunContext"
        :workspaceId="flowAction.workspaceId"
        :flowId="flowAction.flowId"
        :disabled="!isPending"
        @start="handleDevRunStart"
        @stop="handleDevRunStop"
      />

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
  <div v-if="expand && !isPending && (!flowAction.actionParams.command || !parsedActionResult?.Approved)">
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
      <div v-if="showEmptyDiffMessage" class="empty-diff-message">
        No changes since last review
      </div>
      <UnifiedDiffViewer
        v-else
        :diff-string="currentDiffString"
        :default-expanded="false"
        :diff-mode="diffMode"
        :level="level"
      />
    </template>
    <div v-if="parsedActionResult?.Params?.targetBranch">
      Merge into: {{ parsedActionResult.Params.targetBranch }}
    </div>
    <div v-if="/approval/.test(props.flowAction.actionParams.requestKind)">
      <!--p>{{ flowAction.actionParams.requestContent }}</p-->
      <p v-if="!parsedActionResult?.Approved && parsedActionResult?.Content">{{ parsedActionResult.Content }}</p>
    </div>
    <div class="free-form" v-else-if="flowAction.actionParams.requestKind == 'free_form'">
      <p v-if="parsedActionResult?.Content"><b>You: </b>{{ parsedActionResult.Content }}</p>
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
import DiffViewOptions from './DiffViewOptions.vue';
import Select from 'primevue/select';
import DevRunControls from './DevRunControls.vue';

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
const ignoreWhitespace = ref(false);
const diffMode = ref<'unified' | 'split'>('unified');
const diffScope = ref<'all' | 'since_last_review'>('all');
const mergeStrategy = ref<'squash' | 'merge'>('squash');

const hasDiffSinceLastReview = computed(() => {
  const diffSinceLastReview = props.flowAction.actionParams.mergeApprovalInfo?.diffSinceLastReview;
  return typeof diffSinceLastReview === 'string';
});

const diffScopeOptions = computed(() => {
  let options = [
    { label: 'All changes', value: 'all' },
  ]
  if (hasDiffSinceLastReview.value) {
    options.push({ label: 'Changes since last review', value: 'since_last_review' });
  }
  return options;
})

const mergeStrategyOptions = [
  { label: 'Squash merge', value: 'squash' },
  { label: 'Merge commit', value: 'merge' },
];

watch(diffScopeOptions, (newOptions) => {
  if (newOptions.length === 1 && newOptions[0]?.value === 'all') {
    diffScope.value = 'all';
  }
}, { immediate: true });

const currentDiffString = computed(() => {
  if (diffScope.value === 'since_last_review' && hasDiffSinceLastReview.value) {
    return props.flowAction.actionParams.mergeApprovalInfo.diffSinceLastReview;
  }
  return props.flowAction.actionParams.mergeApprovalInfo?.diff;
});

const showEmptyDiffMessage = computed(() => {
  if (diffScope.value !== 'since_last_review' || !hasDiffSinceLastReview.value) {
    return false;
  }
  const diffSinceLastReview = props.flowAction.actionParams.mergeApprovalInfo.diffSinceLastReview;
  return diffSinceLastReview.trim() === '';
});

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

  try {
    const savedIgnoreWhitespace = localStorage.getItem('mergeApproval.diff.ignoreWhitespace');
    if (savedIgnoreWhitespace !== null) {
      ignoreWhitespace.value = savedIgnoreWhitespace === 'true';
    }
    const savedDiffMode = localStorage.getItem('mergeApproval.diff.mode');
    if (savedDiffMode === 'unified' || savedDiffMode === 'split') {
      diffMode.value = savedDiffMode;
    }
    const savedDiffScope = localStorage.getItem('mergeApproval.diff.scope');
    if (savedDiffScope === 'all') {
      diffScope.value = savedDiffScope;
    } else if (savedDiffScope === 'since_last_review' && hasDiffSinceLastReview.value) {
      diffScope.value = savedDiffScope;
    }
    const savedMergeStrategy = localStorage.getItem('mergeApproval.mergeStrategy');
    if (savedMergeStrategy === 'squash' || savedMergeStrategy === 'merge') {
      mergeStrategy.value = savedMergeStrategy;
    }
  } catch (error) {
    console.debug('Failed to load merge approval preferences:', error);
  }

  if (ignoreWhitespace.value && 
      isPending.value && 
      props.flowAction.actionParams.requestKind === 'merge_approval') {
    updateMergeApprovalParams();
  }
});

interface keyable {
    [key: string]: any
}
const parsedActionResult = computed<keyable | null>(() => {
  try {
    return JSON.parse(props.flowAction.actionResult);
  } catch (error) {
    return null;
  }
});

const targetBranch = ref<string | undefined>(parsedActionResult.value?.targetBranch ?? props.flowAction.actionParams.mergeApprovalInfo?.defaultTargetBranch)

watch(targetBranch, async (newBranch, oldBranch) => {
  if (props.flowAction.actionParams.requestKind === 'merge_approval' && 
      isPending.value && 
      oldBranch !== undefined && 
      newBranch !== oldBranch) {
    await updateMergeApprovalParams();
  }
});

watch(ignoreWhitespace, async (newValue, oldValue) => {
  if (props.flowAction.actionParams.requestKind === 'merge_approval' && 
      isPending.value && 
      oldValue !== undefined) {
    await updateMergeApprovalParams();
  }
  
  try {
    localStorage.setItem('mergeApproval.diff.ignoreWhitespace', String(newValue));
  } catch (error) {
    console.debug('Failed to save ignoreWhitespace preference:', error);
  }
});

watch(diffMode, (newValue) => {
  try {
    localStorage.setItem('mergeApproval.diff.mode', newValue);
  } catch (error) {
    console.debug('Failed to save diffMode preference:', error);
  }
});

watch(diffScope, (newValue) => {
  try {
    localStorage.setItem('mergeApproval.diff.scope', newValue);
  } catch (error) {
    console.debug('Failed to save diffScope preference:', error);
  }
});

watch(mergeStrategy, (newValue) => {
  try {
    localStorage.setItem('mergeApproval.mergeStrategy', newValue);
  } catch (error) {
    console.debug('Failed to save mergeStrategy preference:', error);
  }
});

async function handleDevRunStart() {
  await sendDevRunAction('dev_run_start');
}

async function handleDevRunStop() {
  await sendDevRunAction('dev_run_stop');
}

async function sendDevRunAction(actionType: 'dev_run_start' | 'dev_run_stop') {
  try {
    const response = await fetch(`/api/v1/workspaces/${props.flowAction.workspaceId}/flows/${props.flowAction.flowId}/user_action`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ actionType }),
    });

    if (!response.ok) {
      console.error('Failed to send dev run action:', response.status, response.statusText);
    }
  } catch (error) {
    console.error('Network error sending dev run action:', error);
  }
}

async function updateMergeApprovalParams() {
  if (!targetBranch.value) return;

  try {
    const userResponse: UserResponse = {
      params: {
        targetBranch: targetBranch.value,
        ignoreWhitespace: ignoreWhitespace.value,
      },
    };

    const response = await fetch(`/api/v1/workspaces/${props.flowAction.workspaceId}/flow_actions/${props.flowAction.id}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        userResponse: userResponse,
      }),
    });

    if (!response.ok) {
      console.error('Failed to update merge approval params:', response.status, response.statusText);
    }
  } catch (error) {
    console.error('Network error updating merge approval params:', error);
  }
}

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
      ignoreWhitespace: ignoreWhitespace.value,
      diffMode: diffMode.value,
      mergeStrategy: mergeStrategy.value,
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

:deep(.grow-wrap) {
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

.diff-options-row {
  display: flex;
  align-items: center;
  gap: 1rem;
  margin-top: 0.5rem;
}

.diff-options-row label {
  white-space: nowrap;
}

.markdown {
  max-width: 60rem;
}

.markdown :deep(h4) {
  font-size: 120%;
  margin: 2rem 0 1rem;
}

.markdown :deep(h3) {
  font-size: 130%;
  margin: 2rem 0 1rem;
}

.markdown :deep(h2) {
  font-size: 140%;
  margin: 2.25rem 0 1.125rem;
}

.markdown :deep(h1) {
  font-size: 150%;
  margin: 2.5rem 0 1.25rem;
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

.diff-scope-select {
  padding: 0.25rem 0.5rem;
  border-radius: 4px;
  border: 1px solid var(--color-border);
  background-color: var(--color-background);
  color: var(--color-text);
  font-size: 0.875rem;
  cursor: pointer;
}

.diff-scope-select:hover {
  border-color: var(--color-border-hover);
}

.empty-diff-message {
  padding: 1rem;
  text-align: center;
  color: var(--color-text-muted);
  font-style: italic;
  border: 1px dashed var(--color-border);
  border-radius: 4px;
  margin: 0.5rem 0;
}
</style>