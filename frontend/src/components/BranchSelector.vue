<template>
  <Dropdown
    v-model="selectedBranch"
    :options="branches"
    optionLabel="name"
    optionValue="name"
    :loading="isLoadingBranches"
    placeholder="Select Branch"
    class="w-full"
  />
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import Dropdown from 'primevue/dropdown'
import { store } from '../lib/store'
import type { BranchInfo } from '../lib/store'

const props = defineProps<{
  workspaceId: string
  modelValue: string | null
}>()

const emit = defineEmits<{
  (e: 'update:modelValue', value: string | null): void
}>()

// State for branch fetching
const branches = ref<BranchInfo[]>([])
const isLoadingBranches = ref(false)

// Computed property to handle v-model binding
const selectedBranch = computed({
  get: () => props.modelValue,
  set: (value) => emit('update:modelValue', value)
})

// Function to fetch branches
const fetchBranches = async () => {
  if (!props.workspaceId) {
    console.error("Workspace ID is not available to fetch branches.");
    return;
  }
  
  // Check cache first
  const cachedBranches = store.getBranchCache(props.workspaceId);
  if (cachedBranches) {
    branches.value = cachedBranches;
    updateSelectedBranch();
  } else {
    isLoadingBranches.value = true;
  }

  // Fetch fresh data
  try {
    const response = await fetch(`/api/v1/workspaces/${props.workspaceId}/branches`);
    if (!response.ok) {
      throw new Error(`HTTP error! status: ${response.status}`);
    }
    const data = await response.json();
    const freshBranches = data.branches || [];
    branches.value = freshBranches;
    store.setBranchCache(props.workspaceId, freshBranches);
    updateSelectedBranch();
  } catch (error) {
    console.error("Failed to fetch branches:", error);
    if (!cachedBranches) {
      branches.value = []; // Only clear if we had no cache
    }
  } finally {
    isLoadingBranches.value = false;
  }
};

// Helper to handle branch selection logic
const updateSelectedBranch = () => {
  if (!selectedBranch.value && branches.value.length > 0) {
    const current = branches.value.find(b => b.isCurrent);
    if (current) {
      emit('update:modelValue', current.name);
    } else {
      const defaultBranch = branches.value.find(b => b.isDefault);
      if (defaultBranch) {
        emit('update:modelValue', defaultBranch.name);
      }
    }
  }
};

onMounted(() => {
  fetchBranches();
});
</script>