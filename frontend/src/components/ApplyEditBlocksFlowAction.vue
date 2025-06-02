<template>
  <div v-if="expand && actionResult.length > 0" class="apply-edit-blocks-results">
    <div v-for="(result, index) in actionResult" :key="index" class="edit-block-result">
      <h4>
        Edit Block {{ index + 1 }} Result:
        <span v-if="result.didApply" class="result-applied">✅ Applied</span>
        <span v-else class="result-not-applied">❌ Not Applied</span>
      </h4>
      <pre v-if="result.error != ''" class="check-result-message">{{ result.error }}</pre>
      <template v-if="result.finalDiff">
        <UnifiedDiffViewer
          :diff-string="result.finalDiff"
          :default-expanded="true"
        />
      </template>
      <div v-else>
        <p>File: {{ result.originalEditBlock.filePath }}</p>
        <p>Old:</p>
        <pre>{{ result.originalEditBlock.oldLines?.join("\n") }}</pre>
        <p>New:</p>
        <pre>{{ result.originalEditBlock.newLines?.join("\n") }}</pre>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { FlowAction } from '../lib/models';
import UnifiedDiffViewer from './UnifiedDiffViewer.vue';

// FIXME /gen switch to camelCase in backend json struct tags and here
export interface ApplyEditBlockResult {
  didApply: boolean;
  originalEditBlock: {
    filePath: string;
    oldLines: string[];
    newLines: string[];
  };
  error: string,
  checkResult?: {
    success: boolean;
    message: string;
  };
  finalDiff?: string;
}

const props = defineProps({
  expand: {
    type: Boolean,
    required: true,
  },
  flowAction: {
    type: Object as () => FlowAction,
    required: true,
  },
  level: {
    type: Number,
    default: 0,
  },
});

const actionResult = computed(() => {
  let parsedResult: ApplyEditBlockResult[] = [];
  try {
    const rawResult = JSON.parse(props.flowAction.actionResult);
    if (rawResult == null) {
      return [];
    }
    parsedResult = Array.isArray(rawResult) ? rawResult : [rawResult];
  } catch (e: unknown) {
    if (props.flowAction.actionStatus != "started") {
      console.error('Failed to parse action result', e);
    }
  }
  return parsedResult;
});
</script>

<style scoped>
.apply-edit-blocks-results {
  --level: v-bind(level);
  scroll-margin-top: calc(var(--level) * var(--name-height));
  background-color: inherit;
  padding: 0;
}
.edit-block-result {
  background-color: inherit;
}
.edit-block-result + .edit-block-result {
  margin-top: 1rem;
}
.result-applied {
  color: green;
  font-weight: bold;
}
.result-not-applied {
  color: red;
  font-weight: bold;
}
.check-result-message {
  margin-top: 5px;
  font-style: italic;
  padding-left: 40px;
  border-left: 2px solid #ccc;
  padding-top: 5px;
  padding-bottom: 5px;
}

</style>