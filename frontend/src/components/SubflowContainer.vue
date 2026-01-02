<template>
  <div class="subflow-container" :class="{ 'odd': level % 2 === 1, 'container-expanded': accordionState.expanded }" ref="container">
    <div class="subflow-name-container" @click="toggleAccordion" ref="heading">
      <h2 class="subflow-name" :class="{'name-expanded': accordionState.expanded}">
          <span class="caret" :class="{ 'caret-expanded': accordionState.expanded }"></span>
          {{ subflowTree.name }}
          <span v-if="subflowStatus === 'failed'" class="error-indicator">❌</span>
      </h2>
    </div>
    <p v-if="accordionState.expanded && subflowTree.description">{{subflowTree.description}}</p>
    <template v-if="accordionState.expanded">
      <template v-for="(child, index) in subflowTree.children" :key="childKey(child, index)">
        <template v-if="isFlowAction(child)">
          <FlowActionItem v-if="!isStartFlowAction(child)" :flowAction="child" :defaultExpanded="defaultExpanded && index === subflowTree.children.length - 1" :level="level + 1"/>
        </template>
        <SubflowContainer v-else :subflowTree="child" :defaultExpanded="defaultExpanded && index === subflowTree.children.length - 1" :level="level + 1" :subflowsById="subflowsById" />
      </template>
      <div v-if="subflowStatus === 'failed' && subflowResult" class="error-message">
        {{ subflowResult }}
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import FlowActionItem from './FlowActionItem.vue'
import type { FlowAction, SubflowTree, Subflow } from '../lib/models'
import { useEventBus } from '@vueuse/core';

const props = defineProps({
  subflowTree: {
    type: Object as () => SubflowTree,
    required: true,
  },
  defaultExpanded: {
    type: Boolean,
    default: false,
  },
  level: {
    type: Number,
    default: 0,
  },
  subflowsById: {
    type: Object as () => Record<string, Subflow>,
    default: () => ({}),
  },
});

const subflowStatus = computed(() => {
  if (props.subflowTree.id && props.subflowsById[props.subflowTree.id]) {
    return props.subflowsById[props.subflowTree.id].status;
  }
  return null;
});

const subflowResult = computed(() => {
  if (props.subflowTree.id && props.subflowsById[props.subflowTree.id]) {
    return props.subflowsById[props.subflowTree.id].result;
  }
  return null;
});

let wasToggled = false;
const autoExpandThreshold = 2;
const accordionState = ref({ expanded: props.defaultExpanded || props.subflowTree.children.length <= autoExpandThreshold });
watch(() => props.defaultExpanded, (newVal: boolean) => {
  if (!wasToggled) {
    accordionState.value.expanded = newVal || props.subflowTree.children.length <= autoExpandThreshold
  }
  wasToggled = false
})

function isFlowAction(child: FlowAction | SubflowTree): child is FlowAction {
  return 'actionType' in child
}
// TODO /gen remove this function after we delete all these legacy actions
function isStartFlowAction(child: FlowAction | SubflowTree): boolean {
  return isFlowAction(child) && child.actionType === 'subflow_start'
}

const container = ref<HTMLDivElement | null>(null)
function toggleAccordion() {
  accordionState.value.expanded = !accordionState.value.expanded
  wasToggled = true
  nextTick(() => {
    if (accordionState.value.expanded && container.value) {
      const scrollTo = container.value.scrollHeight > window.innerHeight - 100 ? 'start' : 'nearest';
      console.log({scrollTo})
      container.value.scrollIntoView({ behavior: 'instant', block: scrollTo })
    } else if (!accordionState.value.expanded) {
      useEventBus('flow-view-collapse').emit()
    }
  })
}

function childKey(child: FlowAction | SubflowTree, index: number): string {
  if (isFlowAction(child)) {
    return child.id + ":" + child.updated
  } else {
    return child.name + index
  }
}
</script>

<style scoped>
.subflow-container {
  padding: 0 1rem;
  background-color: var(--color-background);
  max-width: 100vw;
  border-bottom: 1px solid var(--color-border);
  --subflow-level: v-bind(level);
  --name-height: 2.5rem;
}

.subflow-container.odd {
  /*background-color: #242424;*/
  background-color: var(--color-background-mute);
}

.subflow-container:has(> .subflow-container:last-child), .subflow-container:has(> .expanded-action:last-child) {
  padding-bottom: 1rem;
}

.subflow-name + p {
  margin-bottom: 5px;
}

.subflow-name-container {
  display: flex;
  align-items: center;
  justify-content: stretch;
  /*
    NOTE: min-height instead of height makes the nested sticky headers slightly
    off when text wraps, but makes this case render more pleasingly.
    Fixing the sticky header positions to account for dynamic height requires
    using IntersectionObserver, which we might do later.
  */
  min-height: var(--name-height);
  padding-left: 0.7rem;
  margin: 0px -1rem;
  background-color: inherit;
  z-index: 1;
  position: sticky;
  cursor: pointer;
}

.subflow-name {
  flex-grow: 1;
  display: block;
  font-size: 1.3rem;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.subflow-name.name-expanded  {
  overflow: visible;
  white-space: wrap;
}

.subflow-name + p {
  margin-top: 0;
}

.subflow-name-container:hover {
  background-color: var(--color-background-hover);
}

/* Calculate top based on level */
.subflow-name-container {
  top: calc(var(--subflow-level, 0) * var(--name-height));
  z-index: calc(100 - var(--subflow-level, 0));
}

.caret {
  display: inline-block;
  width: 0;
  height: 0;
  margin-right: 3px;
  margin-left: 8px;
  margin-bottom: 1px;
  border-top: 6px solid transparent;
  border-bottom: 6px solid transparent;
  border-left: 6px solid currentColor;
  transition: transform 0.1s;
}

.caret-expanded {
  transform: rotate(90deg);
}

.error-indicator {
  margin-left: 0.5rem;
  font-size: 0.9em;
  color: var(--color-error-text);
}

.error-message {
  background-color: var(--color-error-background);
  border: 1px solid var(--color-error-border);
  border-radius: 4px;
  padding: 0.75rem 1rem;
  margin: 0.5rem 0;
  color: var(--color-error-text);
  font-size: 0.9rem;
  white-space: pre-wrap;
  word-break: break-word;
}

/* border styling */
/*
.subflow-container {
  border: 1px solid transparent;
}
.container-expanded {
  border: 1px solid var(--color-border-contrast);
  border-top: 0;
}

.container-expanded > .subflow-name {
  border-top: 1px solid var(--color-border-contrast);
}

.container-expanded + .container-expanded > .subflow-name {
  border-top: 0;
}
.container-expanded:last-child {
  border-bottom: 1px solid var(--color-border-contrast);
}
  */
</style>