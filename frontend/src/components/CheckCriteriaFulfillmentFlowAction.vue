<template>
  <div class="check-criteria-fulfillment">
    <ChatCompletionFlowAction v-if="expand" :flowAction="flowAction" :expand="expand" :jsonTreeDepth="0" />
    <div v-if="criteriaFulfillment">
      <div v-if="expand" class="analysis">
        <strong>Analysis:</strong>
        <vue-markdown :source="criteriaFulfillment.analysis"></vue-markdown>
      </div>
      <div v-if="!criteriaFulfillment.isFulfilled && criteriaFulfillment.feedbackMessage" class="feedback">
        <pre><strong>Feedback:</strong> {{ criteriaFulfillment.feedbackMessage }}</pre>
      </div>
      <div v-if="expand && criteriaFulfillment.confidence != null && criteriaFulfillment.confidence <= 3" class="confidence">
        <pre><strong>Confidence:</strong> {{ criteriaFulfillment.confidence }}/5</pre>
      </div>
      <div v-if="expand && diffString" class="diff-section">
        <UnifiedDiffViewer
          :diff-string="diffString"
          :default-expanded="false"
          :level="level"
        />
      </div>
    </div>
    <div v-else-if="flowAction.actionStatus == 'complete' && expansionCount === null">
      Unable to parse criteria fulfillment data:
      <pre>{{ flowAction.actionResult }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { FlowAction, CriteriaFulfillment } from '@/lib/models';
import { parseCriteriaFulfillment, parseCriteriaExpansionCount } from '@/lib/criteriaFulfillment';
import VueMarkdown from 'vue-markdown-render';
import ChatCompletionFlowAction from './ChatCompletionFlowAction.vue';
import UnifiedDiffViewer from './UnifiedDiffViewer.vue';

const props = defineProps<{
  flowAction: FlowAction;
  expand: boolean;
  level?: number;
}>();

const expansionCount = computed(() => {
  try {
    return parseCriteriaExpansionCount(JSON.parse(props.flowAction.actionResult));
  } catch {
    return null;
  }
});

const criteriaFulfillment = computed<CriteriaFulfillment | null>(() => {
  try {
    return parseCriteriaFulfillment(JSON.parse(props.flowAction.actionResult));
  } catch (error) {
    if (props.flowAction.actionStatus === 'complete') {
      console.error('Error parsing criteria fulfillment data:', error);
    }
    return null;
  }
});

const diffString = computed<string | null>(() => {
  const diff = props.flowAction.actionParams?.diffString;
  return typeof diff === 'string' && diff.trim() !== '' ? diff : null;
});
</script>

<style scoped>
.fulfillment-status,
.confidence,
.analysis,
.feedback {
  margin-bottom: 10px;
}

strong {
  font-weight: bold;
}

.diff-section {
  margin-top: 1rem;
}

/* TODO move this to a single shared component */
.analysis :deep(p), .analysis :deep(ul), .analysis :deep(ol) {
  margin-bottom: 0.5rem;
}
.analysis :deep(ul), .analysis :deep(ol) {
  margin-top: 1rem;
  margin-bottom: 1rem;
}
.analysis :deep(li) {
  margin-bottom: 0.25rem;
}
.analysis :deep(pre) {
  border: 2px solid var(--color-border-contrast);
  padding: 1rem;
  margin-bottom: 1rem;
}
</style>