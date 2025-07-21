<template>
  <div v-if="expand" class="merge-flow-action">
    <div class="action-params">
      <strong>Merge Operation:</strong>
      <span v-if="sourceBranch && targetBranch">
        Merged <code>{{ sourceBranch }}</code> into <code>{{ targetBranch }}</code>
      </span>
      <span v-else>
        <JsonTree :data="flowAction.actionParams" :deep="0" />
      </span>
    </div>
    <div class="action-result" v-if="mergeResult">
      <div v-if="mergeResult.hasConflicts" class="conflict-status error">
        <strong>❌ Conflicts detected</strong>
        <div v-if="mergeResult.conflictDirPath" class="conflict-path">
          Location: <code>{{ mergeResult.conflictDirPath }}</code>
        </div>
      </div>
      <div v-else class="conflict-status success">
        <strong>✅ Merge completed successfully</strong>
      </div>
    </div>
    <div v-else-if="flowAction.actionStatus === 'complete'" class="action-result">
      <pre>{{ flowAction.actionResult }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { FlowAction } from '../lib/models';
import JsonTree from './JsonTree.vue';

interface MergeActivityResult {
  hasConflicts: boolean;
  conflictDirPath: string;
  conflictOnTargetBranch: boolean;
}

const props = defineProps<{
  flowAction: FlowAction;
  expand: boolean;
}>();

const sourceBranch = computed(() => {
  return props.flowAction.actionParams?.sourceBranch || props.flowAction.actionParams?.SourceBranch;
});

const targetBranch = computed(() => {
  return props.flowAction.actionParams?.targetBranch || props.flowAction.actionParams?.TargetBranch;
});

const mergeResult = computed<MergeActivityResult | null>(() => {
  try {
    const parsed = JSON.parse(props.flowAction.actionResult);
    if (parsed && typeof parsed.hasConflicts === 'boolean') {
      return parsed as MergeActivityResult;
    }
    return null;
  } catch (error) {
    if (props.flowAction.actionStatus === 'complete') {
      console.error('Error parsing merge result:', error);
    }
    return null;
  }
});
</script>

<style scoped>
.merge-flow-action {
  margin-top: 1rem;
}

.action-params {
  margin-bottom: 1rem;
}

.action-params code {
  background-color: var(--color-background-mute);
  padding: 0.2em 0.4em;
  border-radius: 0.25rem;
  font-family: var(--font-family-mono);
}

.action-result {
  margin-bottom: 1rem;
}

.conflict-status {
  padding: 0.75rem;
  border-radius: 0.5rem;
  margin-bottom: 0.5rem;
}

.conflict-status.success {
  background-color: var(--color-background-soft);
  border: 1px solid var(--color-border);
  color: var(--color-text);
}

.conflict-status.error {
  background-color: var(--color-background-soft);
  border: 1px solid var(--color-border);
  color: var(--color-text);
}

.conflict-path {
  margin-top: 0.5rem;
  font-size: 0.9em;
}

.conflict-path code {
  background-color: var(--color-background-mute);
  padding: 0.2em 0.4em;
  border-radius: 0.25rem;
  font-family: var(--font-family-mono);
}

pre {
  overflow-x: scroll;
  white-space: pre-wrap;
}
</style>