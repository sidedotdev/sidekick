<template>
  <div class="flow-action" :class="{ 'expanded-action': expand }" ref="container">
    <div class="flow-action-header" :class="{ 'expanded-header': expand }">
      <a @click="toggle()" :class="{'disable-toggle': disableToggle}">
        <h3>
          {{ actionHeading }}
          <span v-if="flowAction?.actionStatus == 'started'">...</span>
          <span v-if="flowAction?.actionStatus == 'failed'">Failed</span>
          <span v-else-if="summary != null" class="action-summary">
            {{ summary.emoji }} {{ summary.text }}
          </span>
        </h3>
      </a>
      <hr>
    </div>
    <div v-if="actionSpecificComponent" :class="{ 'expanded': expand, 'odd': level % 2 === 1 }">
      <component :is="actionSpecificComponent" :flowAction="flowAction" :expand="expand" :level="level + 1"/>
    </div>
    <div v-else-if="expand" :class="{ 'expanded': expand, 'odd': level % 2 === 1 }">
      <h3>Params</h3>
      <JsonTree :data="flowAction.actionParams" />
      <h3>Result</h3>
      <JsonTree :data="actionResult" />
      <div v-if="unparsedActionResult">{{ unparsedActionResult }}</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, nextTick } from 'vue';
import type { FlowAction } from '../lib/models';
import UserRequest from './UserRequest.vue'
import ChatCompletionFlowAction from './ChatCompletionFlowAction.vue'
import RunTestsFlowAction from './RunTestsFlowAction.vue'
import JsonTree from './JsonTree.vue'
import CheckCriteriaFulfillmentFlowAction, { type CriteriaFulfillment } from './CheckCriteriaFulfillmentFlowAction.vue'
import ApplyEditBlocksFlowAction, { type ApplyEditBlockResult } from './ApplyEditBlocksFlowAction.vue';
import PlaintextResultFlowAction from './PlaintextResultFlowAction.vue';
import ToolFlowAction from './ToolFlowAction.vue';
import MergeFlowAction from './MergeFlowAction.vue';
import { useEventBus } from '@vueuse/core';

const props = defineProps({
  flowAction: {
    type: Object as () => FlowAction,
    required: true,
  },
  defaultExpanded: {
    type: Boolean,
    required: false,
  },
  level: {
    type: Number,
    default: 0,
  },
  expand: {
    type: Boolean,
    default: false,
  },
});

const disableToggle = ref(false);
const expand = ref(props.defaultExpanded);
if (props.flowAction.isHumanAction) {
  expand.value = true;
  if (props.flowAction.actionStatus === 'pending') {
    disableToggle.value = true;
  }
}

const container = ref<HTMLDivElement | null>(null)
function toggle() {
  if (disableToggle.value) {
    return
  }
  expand.value = !expand.value
  nextTick(() => {
    if (expand.value && container.value) {
      const scrollTo = container.value.scrollHeight > window.innerHeight - 100 ? 'start' : 'nearest';
      console.log({scrollTo})
      container.value.scrollIntoView({ behavior: 'instant', block: scrollTo })
    } else if (!expand.value) {
      useEventBus('flow-view-collapse').emit()
    }
  })
}

const humanizeText = (text: string): string => {
  return text.replace(/_/g, ' ').replace(/\b\w/g, l => l.toUpperCase());
};

const actionHeading = computed(() => {
  const actionType = props.flowAction.actionType;
  
  switch (actionType) {
    case 'user_request':
      if (props.flowAction.actionStatus === 'complete') {
        return 'Human Input';
      } else {
        return 'AI Asked For Your Input';
      }
    case 'user_request.paused':
      if (props.flowAction.actionStatus === 'complete') {
        return 'Human Input';
      } else {
        return 'Paused';
      }
    case 'user_request.approve_merge':
    case 'user_request.approve.merge':
      return 'Review Changes';
    case 'Approve Dev Requirements':
    case 'user_request.approve.dev_requirements':
        return 'Review Requirements';
    case 'user_request.approve.dev_plan':
        return 'Review Plan';
    case "Get User Guidance":
    case "user_request.guidance":
      if (props.flowAction.actionStatus === 'complete') {
        return 'Human Guidance';
      } else {
        return 'Too Many Iterations: AI Needs Your Help';
      }
    case 'Generate Dev Requirements':
    case 'generate.dev_requirements':
      return 'Generate Requirements'
    case 'Generate Dev Plan':
    case 'generate.dev_plan':
      return 'Generate Plan'
    case 'Generate Code Edits':
    case 'generate.code_edits':
      return 'Generate Edits'
    case 'Get Ranked Repo Summary':
    case 'ranked_repo_summary':
      return 'Ranked Repo Summary';
    case 'Apply Edit Blocks':
    case 'apply_edit_blocks':
      return 'Apply Edits';
    case 'Run Tests':
    case 'RunTests':
    case 'run_tests':
      return 'Tests';
    case 'Check Criteria Fulfillment':
    case 'check_criteria_fulfillment':
      return 'Complete?';
    default:
      // Handle general dot-notation pattern
      if (/^tool_call\./.test(props.flowAction.actionType)){
        return props.flowAction.actionType.replace(/^tool_call\./, 'Tool: ');
      } else if (actionType.includes('.')) {
        const dotIndex = actionType.indexOf('.');
        const beforeDot = actionType.substring(0, dotIndex);
        const afterDot = actionType.substring(dotIndex + 1);
        return `${humanizeText(beforeDot)}: ${humanizeText(afterDot)}`;
      }
      return humanizeText(actionType);
    }
});

const actionSpecificComponent = computed(() => {
  switch (props.flowAction.actionType) {
    case 'user_request':
      return UserRequest
    case 'Get Ranked Repo Summary':
    case 'ranked_repo_summary':
      return PlaintextResultFlowAction
    case 'Check Criteria Fulfillment':
    case 'check_criteria_fulfillment':
      return CheckCriteriaFulfillmentFlowAction
    case 'Apply Edit Blocks':
    case 'apply_edit_blocks':
      return ApplyEditBlocksFlowAction
    case 'Run Tests':
    case 'RunTests':
    case 'run_tests':
      return RunTestsFlowAction
    case 'merge':
      return MergeFlowAction
    default:
      if (props.flowAction.isHumanAction || /^user_request\./.test(props.flowAction.actionType)) {
        return UserRequest
      }
      if (/^generate\./.test(props.flowAction.actionType)) {
        return ChatCompletionFlowAction
      }
      // legacy support for chat completion flow actions with actionType not prefixed with "generate."
      if (props.flowAction.actionParams?.messages && Object.prototype.hasOwnProperty.call(props.flowAction.actionParams, 'temperature')) {
        return ChatCompletionFlowAction
      }
      if (/^(Tool: |tool_call\.)/.test(props.flowAction.actionType)){
        return ToolFlowAction
      }
      return null;
  }
});

const actionResult = computed(() => {
  try {
    const parsed = JSON.parse(props.flowAction.actionResult)
    return parsed
  } catch (e: unknown) {
    return null
  }
})

const unparsedActionResult = computed(() => {
  if (actionResult.value === null) {
    return props.flowAction.actionResult;
  }
  return null
})

interface Summary {
  text: string;
  emoji: string;
}

const summary = computed<Summary | null>(() => {
  if (props.flowAction.actionStatus !== 'complete') {
    return null;
  }

  switch (props.flowAction.actionType) {
    case 'Run Tests':
    case 'run_tests':
    case 'RunTests': {
      // NOTE: shoving non-arrays into an array is to support extremely legacy data
      const results = Array.isArray(actionResult.value) ? actionResult.value : (actionResult.value != null ? [actionResult.value] : []);
      const totalTests = results.length;
      const passedTests = results.filter(result => result.testsPassed).length;
      const allPassed = passedTests === totalTests;
      const allFailed = passedTests === 0;
      return {
        text: allPassed  ? 'Passed' : (allFailed ? 'Failed' : `${passedTests}/${totalTests} tests passed`),
        emoji: allPassed ? '✅' : '❌',
      };
    }

    case 'apply_edit_blocks':
    case 'Apply Edit Blocks': {
      const editResults = actionResult.value as ApplyEditBlockResult[] | null;
      if (editResults == null) {
        return {
          text: 'No edits were generated',
          emoji: '🟡',
        };
      }
      const totalEdits = editResults.length;
      const successfulEdits = editResults.filter(result => result.didApply && (!result.checkResult || result.checkResult.success)).length;
      const allApplied = successfulEdits === totalEdits;
      return {
        text: allApplied ? `${successfulEdits} edit${ successfulEdits !== 1 ? 's' : ''} applied` : `${successfulEdits}/${totalEdits} edits applied`,
        emoji: allApplied ? '✅' : (successfulEdits > 0 ? '🟡' : '❌'),
      };
    }

    case 'check_criteria_fulfillment':
    case 'Check Criteria Fulfillment': {
      try {
        const criteriaFulfillment = JSON.parse(actionResult.value?.toolCalls[0]?.arguments || "null") as CriteriaFulfillment | null;
        if (criteriaFulfillment === null) {
          return null;
        }
        return {
          text: criteriaFulfillment.isFulfilled ? 'Yes' : 'No',
          emoji: criteriaFulfillment.isFulfilled ? '✅' : '❌',
        };
      } catch (error) {
        console.error('Error parsing criteria fulfillment data:', error);
        return null;
      }
    }

    case 'merge': {
      try {
        const mergeResult = actionResult.value;
        if (mergeResult === null || mergeResult === undefined) {
          return null;
        }
        return {
          text: mergeResult.hasConflicts ? 'Conflicts' : '',
          emoji: mergeResult.hasConflicts ? '❌' : '✅',
        };
      } catch (error) {
        console.error('Error parsing merge result data:', error);
        return null;
      }
    }

    case 'user_request': {
      try {
        if (actionResult.value === null || actionResult.value === undefined) {
          return null;
        }
        if (typeof actionResult.value.Approved === 'boolean') {
          return {
            text: actionResult.value.Approved ? 'Approved' : 'Rejected',
            emoji: actionResult.value.Approved ? '✅' : '❌',
          };
        }
        return null;
      } catch (error) {
        console.error('Error parsing user request result data:', error);
        return null;
      }
    }

    default: {
      return null;
    }
  }
})
</script>

<style scoped>
.action-summary {
  font-size: 0.85em;
  margin-left: 0px;
  padding: 2px 5px;
  border-radius: 4px;
  color: var(--color-text);
}

.flow-action {
  --name-height: 2.5rem;
  --expanded-left-margin: 0;
  --pad: 1rem;
  --level: v-bind(level);
  scroll-margin-top: calc(var(--level) * var(--name-height));
  background-color: inherit;
}

.flow-action-header {
  position: sticky;
  z-index: calc(50 - var(--level, 0));
  background-color: inherit;
  top: calc(v-bind(level) * var(--name-height) - 1px);
  height: var(--name-height);
  display: flex;
  flex-direction: column;
  justify-content: space-between;
  margin-left: calc(-1 * var(--pad));
}
.flow-action-header hr {
  display: none;
}

.flow-action-header.expanded-header hr {
  display: block;
  border: 0;
  border-bottom: 1px solid var(--color-border);
  margin-left: var(--pad);
}

.flow-action a {
  padding-left: var(--pad);
  height: var(--name-height);
  display: flex;
  align-items: center;
}

h3 + p {
  white-space: pre-wrap;
  padding-left: var(--pad);
}

a h3 {
  cursor: pointer;
  font-size: 1.0rem;
}

a:hover {
  background-color: var(--color-background-hover);
}
a.disable-toggle, a.disable-toggle * {
  cursor: default;
  color: var(--color-text);
}
a.disable-toggle:hover {
  background-color: inherit;
  filter: none;
}

.expanded {
  margin-left: var(--expanded-left-margin);
  padding: 1rem;
  border: 1px solid var(--color-border);
  border-top: 0;
  background-color: var(--color-background);
}

.expanded.odd {
  /*background-color: #242424;*/
  background-color: var(--color-background-mute);
}

.expanded.odd :deep(.diff-file .file-header:not(:hover)) {
  background-color: var(--color-background);
}

.flow-action:has(.expanded:last-child):has(+ .subflow-container) {
  padding-bottom: 1rem;
}
</style>