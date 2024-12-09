<template>
  <div class="check-criteria-fulfillment">
    <div v-if="criteriaFulfillment">
      <div v-if="expand" class="analysis">
        <strong>Analysis:</strong>
        <vue-markdown :source="criteriaFulfillment.analysis"></vue-markdown>
      </div>
      <div v-if="!criteriaFulfillment.isFulfilled && criteriaFulfillment.feedbackMessage" class="feedback">
        <pre><strong>Feedback:</strong> {{ criteriaFulfillment.feedbackMessage }}</pre>
      </div>
      <div v-if="expand && criteriaFulfillment.confidence <= 3" class="confidence">
        <pre><strong>Confidence:</strong> {{ criteriaFulfillment.confidence }}/5</pre>
      </div>
    </div>
    <div v-else-if="flowAction.actionStatus == 'complete'">
      Unable to parse criteria fulfillment data:
      <pre>{{ flowAction.actionResult }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { ChatCompletionMessage, FlowAction } from '@/lib/models';
import VueMarkdown from 'vue-markdown-render';

export interface CriteriaFulfillment {
  whatWasActuallyDone: string;
  analysis: string;
  isFulfilled: boolean;
  confidence: number;
  feedbackMessage?: string;
}

const props = defineProps<{
  flowAction: FlowAction;
  expand: boolean;
}>();

const criteriaFulfillment = computed<CriteriaFulfillment | null>(() => {
  try {
    const parsedActionResult = JSON.parse(props.flowAction.actionResult) as ChatCompletionMessage
    return JSON.parse(parsedActionResult.toolCalls[0].arguments || "null") as CriteriaFulfillment | null
  } catch (error) {
    if (props.flowAction.actionStatus === 'complete') {
      console.error('Error parsing criteria fulfillment data:', error);
    }
    return null;
  }
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