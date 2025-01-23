<template>
  <form @submit.prevent="submitWorkspace">
    <div>
      <label for="name">Workspace Name:</label>
      <input id="name" v-model="name" required>
    </div>
    <div>
      <label for="localRepoDir">Local Repository Directory:</label>
      <input id="localRepoDir" v-model="localRepoDir" required>
    </div>
    <div>
      <h3>LLMs</h3>
      <ModelConfigSelector
        v-model="llmConfig.defaults"
        type="llm"
      />
      <ExpandableSection
        v-model="llmAdvancedExpanded"
        title="Advanced Settings"
      >
        <template v-for="(configs, useCase) in llmConfig.useCaseConfigs" :key="useCase">
          <ModelConfigSelector
            v-model="llmConfig.useCaseConfigs[String(useCase)]"
            :selected-use-case="String(useCase)"
            type="llm"
            use-case-mode
            @remove-use-case="removeUseCaseConfig('llm', String(useCase))"
          />
        </template>
        <ModelConfigSelector
          :model-value="[{ provider: '', model: '' }]"
          type="llm"
          use-case-mode
          @update:selected-use-case="addUseCaseConfig('llm', $event)"
          @update:model-value="updateEmptyUseCaseConfig($event)"
        />
      </ExpandableSection>
    </div>
    <div>
      <h3>Embeddings</h3>
      <!-- Note: Embeddings intentionally do not support use case configs -->
      <ModelConfigSelector
        v-model="embeddingConfig.defaults"
        type="embedding"
      />
    </div>
    <button type="submit">{{ isEditing ? 'Update' : 'Create' }} Workspace</button>
  </form>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import ModelConfigSelector from '@/components/ModelConfigSelector.vue';
import ExpandableSection from '@/components/ExpandableSection.vue';
import type { Workspace, LLMConfig, EmbeddingConfig, ModelConfig } from '@/lib/models';

const props = defineProps<{
  workspace: Workspace;
}>();

const emit = defineEmits<{
  (event: 'created', id: string): void;
  (event: 'updated', id: string): void;
}>();

const name = ref('');
const localRepoDir = ref('');
const llmConfig = ref<LLMConfig>({ 
  defaults: props.workspace.llmConfig?.defaults?.length ? [...props.workspace.llmConfig.defaults] : [{ provider: '', model: '' }], 
  useCaseConfigs: props.workspace.llmConfig?.useCaseConfigs ? { ...props.workspace.llmConfig.useCaseConfigs } : {} 
});
const embeddingConfig = ref<EmbeddingConfig>({ 
  defaults: props.workspace.embeddingConfig?.defaults?.length ? [...props.workspace.embeddingConfig.defaults] : [{ provider: '', model: '' }], 
  useCaseConfigs: props.workspace.embeddingConfig?.useCaseConfigs ? { ...props.workspace.embeddingConfig.useCaseConfigs } : {} 
});

const llmAdvancedExpanded = ref(false);
const embeddingAdvancedExpanded = ref(false);

const isEditing = computed(() => !!props.workspace.id);

const addUseCaseConfig = (configType: 'llm' | 'embedding', useCase: string) => {
  const config = configType === 'llm' ? llmConfig : embeddingConfig;
  if (!config.value.useCaseConfigs[useCase]) {
    config.value.useCaseConfigs[useCase] = [{ provider: '', model: '' }];
  }
};

const removeUseCaseConfig = (configType: 'llm' | 'embedding', useCase: string) => {
  const config = configType === 'llm' ? llmConfig : embeddingConfig;
  delete config.value.useCaseConfigs[useCase];
};

const updateEmptyUseCaseConfig = (newConfig: ModelConfig[]) => {
  // This handles the "Add Fallback" button for the empty use case config selector
  if (newConfig.length > 1) {
    const lastConfig = newConfig[newConfig.length - 1];
    const emptyConfig = { provider: '', model: '' };
    return [emptyConfig];
  }
};

onMounted(() => {
  if (props.workspace) {
    name.value = props.workspace.name;
    localRepoDir.value = props.workspace.localRepoDir;
  }
});

const submitWorkspace = async () => {
  const formData: Omit<Workspace, 'id'> = {
    name: name.value,
    localRepoDir: localRepoDir.value,
    llmConfig: llmConfig.value,
    embeddingConfig: embeddingConfig.value
  };

  try {
    const url = isEditing.value ? `/api/v1/workspaces/${props.workspace?.id}` : '/api/v1/workspaces';
    const method = isEditing.value ? 'PUT' : 'POST';

    const response = await fetch(url, {
      method,
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(formData)
    });

    if (response.ok) {
      const newWorkspace: Workspace = (await response.json()).workspace;
      if (isEditing.value) {
        emit('updated', newWorkspace.id as string);
      } else {
        emit('created', newWorkspace.id as string);
      }
    } else {
      console.error(`Failed to ${isEditing.value ? 'update' : 'create'} workspace:`, response.status);
    }
  } catch (error) {
    console.error(`Failed to ${isEditing.value ? 'update' : 'create'} workspace:`, error);
  }
};
</script>

<style scoped>
form {
  display: flex;
  flex-direction: column;
  gap: 1rem;
}

.config-item {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-bottom: 0.5rem;
}

.add-btn, .remove-btn {
  padding: 0.25rem 0.5rem;
  font-size: 0.875rem;
  cursor: pointer;
}

.add-btn {
  margin-top: 0.5rem;
}

.remove-btn {
  background-color: var(--color-danger);
  color: var(--color-text-inverse);
  border: none;
  border-radius: 0.25rem;
}
</style>