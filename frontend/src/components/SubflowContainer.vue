<template>
  <div class="subflow-container" :class="{ 'odd': level % 2 === 1, 'container-expanded': accordionState.expanded }" ref="container">
    <div class="subflow-name-container" @click="toggleAccordion" ref="heading">
      <h2 class="subflow-name">
          <span class="caret" :class="{ 'caret-expanded': accordionState.expanded }"></span>
          {{ subflowTree.name }}
      </h2>
    </div>
    <p v-if="accordionState.expanded && subflowTree.description">{{subflowTree.description}}</p>
    <template v-if="accordionState.expanded">
      <template v-for="(child, index) in subflowTree.children" :key="childKey(child, index)">
        <template v-if="isFlowAction(child)">
          <FlowActionItem v-if="!isStartFlowAction(child)" :flowAction="child" :defaultExpanded="defaultExpanded && index === subflowTree.children.length - 1" :level="level + 1"/>
        </template>
        <SubflowContainer v-else :subflowTree="child" :defaultExpanded="defaultExpanded && index === subflowTree.children.length - 1" :level="level + 1" />
      </template>
    </template>
  </div>
</template>

<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'
import FlowActionItem from './FlowActionItem.vue'
import type { FlowAction, SubflowTree } from '../lib/models'
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
});

let wasToggled = false;
// FIXME autoExpandThreshold is set to 4 based on flow actions being doubled
// inadvertently (we're not updating correctly on started -> complete), we
// really just want 2 once we fix that bug. the doubling doesn't show up due to
// the childKey function used as :key in the template's v-for
const autoExpandThreshold = 4;
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
    return child.id
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
  height: var(--name-height);
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