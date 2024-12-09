<template>
  <div v-if="expand" class="test-results">
    <div v-for="(testResult, index) in actionResult" :key="index">
        <h4>
            Test Results:
            <span v-if="testResult.testsPassed" class="result-passed">Passed</span>
            <span v-else class="result-failed">Failed</span>
        </h4>
        <pre>{{ testResult.output }}</pre>
    </div>
    <div v-if="!actionResult">
        <h3>Running Tests:</h3>
        <ul>
            <li v-for="(command, index) in params.testCommands" :key="index">
                <strong>Command:</strong> {{ command.command }}
                <span><strong>Working Directory:</strong> {{ command.workingDir }}</span>
            </li>
        </ul>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, defineProps, reactive } from 'vue';
import type { FlowAction } from '../lib/models';

interface RunTestsParams {
  testCommands: Array<{
    command: string;
    workingDir: string;
  }>;
}

interface RunTestsResult {
  testsPassed: boolean;
  output: string;
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
});

const params = props.flowAction.actionParams as RunTestsParams;
const errorLoadingResults = reactive<{ status: boolean }>({ status: false });

const actionResult = computed(() => {
  let result: RunTestsResult[] | null = null;
  try {
    result = JSON.parse(props.flowAction.actionResult);
    if (!Array.isArray(result)) {
      // support for legacy single test result
      result = [result as unknown as RunTestsResult];
    }
  } catch (e: unknown) {
    console.error('Failed to parse action result', e);
  }
  return result;
});

if (!actionResult.value) {
  errorLoadingResults.status = true;
}
</script>

<style scoped>
/* Styles specific to this component */
.result-passed {
  color: green;
}
.result-failed {
  color: red;
}
</style>