<template>
  <section v-if="action" class="web-search-block">
    <header class="web-search-header">
      <strong>{{ action.title }}</strong>
      <span v-if="action.status" class="web-search-status">{{ action.status }}</span>
    </header>
    <ul v-if="action.queries" class="web-search-queries">
      <li v-for="query in action.queries" :key="query">{{ query }}</li>
    </ul>
    <p v-if="action.pattern" class="web-search-pattern">Find: <code>{{ action.pattern }}</code></p>
    <a v-if="action.url" :href="action.url" target="_blank" rel="noopener noreferrer">{{ action.url }}</a>
  </section>
  <section v-else-if="searchResults" class="web-search-block web-search-results">
    <header class="web-search-header">
      <strong>Web search results</strong>
      <span v-if="searchResults.isError" class="web-search-status">error</span>
    </header>
    <p v-if="searchResults.content" class="web-search-content">{{ searchResults.content }}</p>
    <ol v-if="searchResults.results.length" class="web-search-result-list">
      <li v-for="(result, index) in searchResults.results" :key="index">
        <a :href="result.url" target="_blank" rel="noopener noreferrer">{{ result.title || result.url }}</a>
        <span v-if="result.pageAge" class="web-search-page-age">{{ result.pageAge }}</span>
        <span v-if="result.title && result.title !== result.url" class="web-search-result-url">{{ result.url }}</span>
      </li>
    </ol>
    <p v-else-if="!searchResults.content" class="web-search-content">No results</p>
  </section>
  <div v-else class="llm2-unknown-block">
    <p class="unknown-block-header">Unknown Block ({{ block.type }}):</p>
    <JsonTree :deep="jsonTreeDepth" :data="block" />
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { parseWebSearchBlock, parseWebSearchResultBlock } from '../lib/webSearch'
import JsonTree from './JsonTree.vue'

const props = withDefaults(defineProps<{
  block: { type?: string }
  jsonTreeDepth?: number
}>(), {
  jsonTreeDepth: 1
})

const action = computed(() => parseWebSearchBlock(props.block))
const searchResults = computed(() => action.value ? null : parseWebSearchResultBlock(props.block))
</script>

<style scoped>
.web-search-block,
.llm2-unknown-block {
  margin-bottom: 0.5rem;
  padding: 0.75rem;
  border-radius: 0.375rem;
  background: var(--color-background-soft);
  overflow-wrap: anywhere;
}

.web-search-block {
  border-left: 0.2rem solid var(--color-border-contrast);
}

.web-search-header {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 0.5rem;
}

.web-search-status {
  color: var(--color-text-2);
  font-size: 0.85em;
  border: 0.0625rem solid var(--color-border-contrast);
  border-radius: 0.25rem;
  padding: 0.1em 0.4em;
}

.web-search-queries {
  margin: 0.5rem 0 0;
  padding-left: 1.25rem;
  white-space: pre-wrap;
}

.web-search-pattern {
  margin: 0.5rem 0;
  white-space: pre-wrap;
}

.web-search-content {
  margin: 0.5rem 0 0;
  white-space: pre-wrap;
}

.web-search-result-list {
  margin: 0.5rem 0 0;
  padding-left: 1.5rem;
}

.web-search-result-list li {
  margin-bottom: 0.25rem;
}

.web-search-result-list a {
  margin-top: 0;
  margin-right: 0.5rem;
}

.web-search-page-age,
.web-search-result-url {
  color: var(--color-text-2);
  font-size: 0.85em;
}

.web-search-page-age {
  margin-right: 0.5rem;
}

.web-search-result-url {
  display: block;
}

a {
  display: inline-block;
  margin-top: 0.25rem;
  color: var(--color-link);
  text-decoration: underline;
}

.unknown-block-header {
  font-weight: bold;
  margin: 0 0 0.25rem;
}
</style>