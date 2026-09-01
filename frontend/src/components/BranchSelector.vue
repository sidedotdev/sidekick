<template>
  <div>
    <FuzzySelect
      v-model="selectedBranchValue"
      :options="branchOptions"
      optionLabel="label"
      optionValue="value"
      :appendOption="createOption"
      :filterPlaceholder="filterPlaceholder"
      :loading="isLoadingBranches"
      placeholder="Select Branch"
      class="w-full"
      @filter="filterText = $event"
    />
    <small v-if="!isLoadingBranches && branches.length === 0">No branches found or failed to load.</small>

    <div v-if="showCreateDialog" class="create-branch-overlay" @click.self="cancelCreate">
      <div class="create-branch-dialog">
        <h3>Create branch</h3>
        <p class="create-branch-name">{{ pendingBranchName }}</p>
        <label class="create-branch-label">Base it on</label>
        <FuzzySelect
          v-model="baseBranch"
          :options="branchOptions"
          optionLabel="label"
          optionValue="value"
          filterPlaceholder="Search branches"
          placeholder="Select Branch"
        />
        <p v-if="createError" class="create-branch-error">{{ createError }}</p>
        <div class="create-branch-actions">
          <button type="button" class="create-branch-cancel" @click="cancelCreate">Cancel</button>
          <button
            type="button"
            class="create-branch-confirm"
            :disabled="!baseBranch || isCreating"
            @click="confirmCreate"
          >{{ isCreating ? 'Creating...' : 'Create' }}</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { store } from '../lib/store'
import type { BranchInfo } from '../lib/store'
import FuzzySelect from './FuzzySelect.vue'

const props = withDefaults(defineProps<{
  workspaceId: string
  modelValue: string | undefined | null
  allowCreate?: boolean
}>(), {
  allowCreate: false,
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string | null): void
}>()

// The pending branch name is encoded into the option value so it survives the
// dropdown clearing its filter text when it closes.
const CREATE_OPTION_PREFIX = '__create_branch__:'

// State for branch fetching
const branches = ref<BranchInfo[]>([])
const isLoadingBranches = ref(false)

const filterText = ref('')
const showCreateDialog = ref(false)
const pendingBranchName = ref('')
const baseBranch = ref<string | null>(null)
const createError = ref('')
const isCreating = ref(false)

const branchOptions = computed(() => branches.value.map(branch => ({
  label: branch.name,
  value: branch.name,
})))

const filterPlaceholder = computed(() =>
  props.allowCreate ? 'Search or create branch' : 'Search branches')

const createOption = computed(() => {
  if (!props.allowCreate) return null
  const name = filterText.value.trim()
  if (!name || branches.value.some(branch => branch.name === name)) return null
  return { label: `Create branch "${name}"`, value: `${CREATE_OPTION_PREFIX}${name}` }
})

// Computed property to handle v-model binding with null safety
const selectedBranchValue = computed({
  get: () => props.modelValue || '',
  set: (value: string) => {
    if (value?.startsWith(CREATE_OPTION_PREFIX)) {
      openCreateDialog(value.slice(CREATE_OPTION_PREFIX.length))
      return
    }
    emit('update:modelValue', value || null)
  }
})

const openCreateDialog = (name: string) => {
  pendingBranchName.value = name
  createError.value = ''
  baseBranch.value = props.modelValue
    || branches.value.find(branch => branch.isCurrent)?.name
    || branches.value.find(branch => branch.isDefault)?.name
    || null
  showCreateDialog.value = true
}

const cancelCreate = () => {
  showCreateDialog.value = false
  pendingBranchName.value = ''
  createError.value = ''
}

const confirmCreate = async () => {
  if (!pendingBranchName.value || !baseBranch.value || isCreating.value) return

  isCreating.value = true
  createError.value = ''
  try {
    const response = await fetch(`/api/v1/workspaces/${props.workspaceId}/branches`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: pendingBranchName.value, baseBranch: baseBranch.value }),
    })
    const data = await response.json().catch(() => null)
    if (!response.ok) {
      throw new Error(data?.error || `HTTP error! status: ${response.status}`)
    }
    const createdName = data?.branch?.name || pendingBranchName.value
    showCreateDialog.value = false
    pendingBranchName.value = ''
    await fetchBranches()
    emit('update:modelValue', createdName)
  } catch (error) {
    createError.value = error instanceof Error ? error.message : 'Failed to create branch'
  } finally {
    isCreating.value = false
  }
}

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
  if (!props.modelValue && branches.value.length > 0) {
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

<style scoped>
.create-branch-overlay {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background-color: rgba(0, 0, 0, 0.5);
  display: flex;
  justify-content: center;
  align-items: center;
  z-index: 2000;
}

.create-branch-dialog {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  background-color: var(--color-modal-background);
  border: 1px solid var(--color-border-contrast);
  border-radius: 0.5rem;
  padding: 1.5rem;
  min-width: 20rem;
}

.create-branch-dialog h3 {
  margin: 0;
  color: var(--color-heading);
}

.create-branch-name {
  margin: 0;
  font-family: monospace;
  color: var(--color-text);
  overflow-wrap: anywhere;
}

.create-branch-label {
  color: var(--color-text-2);
  font-size: 0.875rem;
}

.create-branch-error {
  margin: 0;
  color: var(--color-error-text);
  font-size: 0.875rem;
}

.create-branch-actions {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
  margin-top: 0.5rem;
}

.create-branch-actions button {
  padding: 0.5rem 1rem;
  border-radius: 0.25rem;
  border: 1px solid var(--color-border-contrast);
  cursor: pointer;
}

.create-branch-cancel {
  background-color: transparent;
  color: var(--color-text-2);
}

.create-branch-cancel:hover {
  color: var(--color-text);
}

.create-branch-confirm {
  background-color: var(--color-cta-button-bg);
  color: var(--color-cta-button-text);
}

.create-branch-confirm:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}
</style>