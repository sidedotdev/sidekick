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

const actionHeading = computed(() => {
  switch (props.flowAction.actionType) {
    case 'user_request':
      if (props.flowAction.actionStatus === 'complete') {
        return 'Human Input';
      } else {
        return 'AI Asked For Your Input';
      }
    case "Get User Guidance":
      if (props.flowAction.actionStatus === 'complete') {
        return 'Human Guidance';
      } else {
        return 'Too Many Iterations: AI Needs Your Help';
      }
    case 'Approve Dev Requirements':
        return 'Human Review';
    case 'Generate Dev Requirements':
      return 'Generate Requirements'
    case 'Generate Dev Plan':
      return 'Generate Plan'
    case 'Generate Code Edits':
      return 'Generate Edits'
    case 'Apply Edit Blocks':
      return 'Apply Edits';
    case 'Run Tests':
    case 'RunTests':
      return 'Tests';
    case 'Check Criteria Fulfillment':
      return 'Complete?';
    default:
      return props.flowAction.actionType;
    }
});

const actionSpecificComponent = computed(() => {
  switch (props.flowAction.actionType) {
    case 'user_request':
      return UserRequest
    case 'Get Ranked Repo Summary':
      return PlaintextResultFlowAction
    case 'Check Criteria Fulfillment':
      return CheckCriteriaFulfillmentFlowAction
    case 'Apply Edit Blocks':
      return ApplyEditBlocksFlowAction
    case 'Run Tests':
    case 'RunTests':
      return RunTestsFlowAction
    default:
      if (props.flowAction.isHumanAction) {
        return UserRequest
      }
      if (props.flowAction.actionParams?.messages && Object.prototype.hasOwnProperty.call(props.flowAction.actionParams, 'temperature')) {
        return ChatCompletionFlowAction
      }
      if (/^Tool: /.test(props.flowAction.actionType)){
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

.flow-action:has(.expanded:last-child):has(+ .subflow-container) {
  padding-bottom: 1rem;
}
</style>