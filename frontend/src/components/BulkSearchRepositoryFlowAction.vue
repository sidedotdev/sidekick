<template>
  <div v-if="expand" class="tool-flow-action">
    <div v-if="summary" class="action-summary-section">
      <strong>Summary:</strong> {{ summary }}
    </div>
    <div class="action-params">
      Params: <JsonTree :data="flowAction.actionParams" :deep="0" />
    </div>
    <div class="action-result">
      <pre v-if="toolResponse">{{ toolResponse }}</pre>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { FlowAction } from '../lib/models';
import JsonTree from './JsonTree.vue'

const props = defineProps<{
  flowAction: FlowAction,
  expand: boolean,
  level?: number
}>()

const summary = computed(() => {
  try {
    const params = props.flowAction.actionParams;
    if (!params || !params.searches || !Array.isArray(params.searches)) {
      return null;
    }

    const globGroups: Record<string, string[]> = {};
    
    for (const search of params.searches) {
      if (!search.path_glob || !search.search_term) continue;
      
      if (!globGroups[search.path_glob]) {
        globGroups[search.path_glob] = [];
      }
      globGroups[search.path_glob].push(search.search_term);
    }

    const parts = Object.entries(globGroups).map(([pathGlob, searchTerms]) => 
      `${pathGlob}: ${searchTerms.join(', ')}`
    );

    return parts.join('; ');
  } catch (error) {
    console.error('Error computing bulk_search_repository summary:', error);
    return null;
  }
});

const toolResponse = computed(() => {
  try {
    const parsed = JSON.parse(props.flowAction.actionResult)
    if (parsed && parsed.Response) {
      return parsed.Response
    }
    return null
  } catch (error) {
    console.error('Error parsing action result:', error)
    return null
  }
})
</script>

<style scoped>
.tool-flow-action {
  margin-top: 0.625rem;
}

.action-summary-section {
  margin-top: 0.625rem;
}

.action-params {
  margin-top: 0.625rem;
}
</style>