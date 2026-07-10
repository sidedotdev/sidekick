<template>
  <div v-if="expand && actionResult.length > 0" class="apply-edit-blocks-results">
    <div v-for="(result, index) in actionResult" :key="index" class="edit-block-result">
      <h4>
        {{ editBlockLabel(result) }} Result:
        <span v-if="result.didApply" class="result-applied">✅ Applied</span>
        <span v-else class="result-not-applied">❌ Not Applied</span>
      </h4>
      <pre v-if="result.error != ''" class="check-result-message">{{ result.error }}</pre>
      <template v-if="result.finalDiff">
        <UnifiedDiffViewer
          :diff-string="result.finalDiff"
          :default-expanded="true"
          :level="level"
        />
      </template>
      <div v-else-if="result.originalEditBlocks?.[0]?.filePath">
        <p>File: {{ result.originalEditBlocks[0].filePath }}</p>
        <p>Old:</p>
        <pre>{{ result.originalEditBlocks[0].oldLines?.join("\n") }}</pre>
        <p>New:</p>
        <pre>{{ result.originalEditBlocks[0].newLines?.join("\n") }}</pre>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { FlowAction } from '../lib/models';
import UnifiedDiffViewer from './UnifiedDiffViewer.vue';

interface EditBlockInfo {
  filePath: string;
  oldLines: string[];
  newLines: string[];
  sequenceNumber?: number;
}

export interface ApplyEditBlockResult {
  didApply: boolean;
  originalEditBlocks: EditBlockInfo[];
  error: string,
  checkResult?: {
    success: boolean;
    message: string;
  };
  finalDiff?: string;
}

function formatSequenceNumbers(nums: number[]): string {
  if (nums.length === 0) return '';
  const sorted = [...nums].sort((a, b) => a - b);
  const spans: { start: number; end: number }[] = [{ start: sorted[0], end: sorted[0] }];
  for (let i = 1; i < sorted.length; i++) {
    const last = spans[spans.length - 1];
    if (sorted[i] === last.end + 1) {
      last.end = sorted[i];
    } else {
      spans.push({ start: sorted[i], end: sorted[i] });
    }
  }
  return spans
    .map(s => {
      if (s.end - s.start >= 2) return `${s.start}-${s.end}`;
      const parts: number[] = [];
      for (let n = s.start; n <= s.end; n++) parts.push(n);
      return parts.join(',');
    })
    .join(',');
}

function editBlockLabel(result: ApplyEditBlockResult): string {
  if (result.originalEditBlocks && result.originalEditBlocks.length > 1) {
    const seqNums = result.originalEditBlocks
      .map(b => b.sequenceNumber ?? 0)
      .filter(n => n > 0);
    if (seqNums.length > 1) {
      return `Edit Blocks ${formatSequenceNumbers(seqNums)}`;
    }
    return `Edit Blocks (${result.originalEditBlocks.length} blocks)`;
  }
  return `Edit Block ${result.originalEditBlocks?.[0]?.sequenceNumber ?? '?'}`;
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