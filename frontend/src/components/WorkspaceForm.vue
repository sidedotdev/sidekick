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
      <h3>Configuration Mode</h3>
      <div class="config-mode-options">
        <label class="radio-option">
          <input type="radio" v-model="configMode" value="local" name="configMode">
          Defer to local config
        </label>
        <label class="radio-option">
          <input type="radio" v-model="configMode" value="workspace" name="configMode">
          Use workspace only
        </label>
        <label class="radio-option">
          <input type="radio" v-model="configMode" value="merge" name="configMode">
          Merge configs
        </label>
      </div>
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
            @update:selected-use-case="(newUseCase) => updateUseCaseConfig('llm', String(useCase), newUseCase)"
            @remove-use-case="removeUseCaseConfig('llm', String(useCase))"
          />
        </template>
      </ExpandableSection>
    </div>
    <div>
      <h3>Embeddings</h3>
      <!-- Note: Embeddings do not support use case configs yet, even though
      embedding config does, so don't show them in the UI -->
      <ModelConfigSelector
        v-model="embeddingConfig.defaults"
        type="embedding"
      />
    </div>
    <button type="submit">{{ isEditing ? 'Update' : 'Create' }} Workspace</button>
  </form>
</template>

<script setup lang="ts">
import { ref, computed, onMounted, nextTick } from 'vue';
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
const configMode = ref('merge');
const getEmptyUseCaseConfigs = () => { return {'': [{ provider: '', model: '' }]}}
const llmConfig = ref<LLMConfig>({ 
  defaults: props.workspace.llmConfig?.defaults?.length ? [...props.workspace.llmConfig.defaults] : [{ provider: '', model: '' }], 
  useCaseConfigs: props.workspace.llmConfig?.useCaseConfigs ? { ...props.workspace.llmConfig.useCaseConfigs } : getEmptyUseCaseConfigs() 
});
if (!Object.keys(llmConfig.value.useCaseConfigs).length) {
  llmConfig.value.useCaseConfigs = getEmptyUseCaseConfigs() 
}
const embeddingConfig = ref<EmbeddingConfig>({ 
  defaults: props.workspace.embeddingConfig?.defaults?.length ? [...props.workspace.embeddingConfig.defaults] : [{ provider: '', model: '' }], 
  useCaseConfigs: props.workspace.embeddingConfig?.useCaseConfigs ? { ...props.workspace.embeddingConfig.useCaseConfigs } : getEmptyUseCaseConfigs() 
});
if (!Object.keys(embeddingConfig.value.useCaseConfigs).length) {
  embeddingConfig.value.useCaseConfigs = getEmptyUseCaseConfigs() 
}

const llmAdvancedExpanded = ref(false);

const isEditing = computed(() => !!props.workspace.id);

const updateUseCaseConfig = (configType: 'llm' | 'embedding', oldUseCase: string, newUseCase: string | undefined) => {
  const config = configType === 'llm' ? llmConfig : embeddingConfig;
  if (newUseCase) {
    let newModelConfigs = [{ provider: '', model: '' }]
    if (config.value.useCaseConfigs[oldUseCase]) {
      newModelConfigs = config.value.useCaseConfigs[oldUseCase]
      delete config.value.useCaseConfigs[oldUseCase]
    }
    if (!config.value.useCaseConfigs[newUseCase]) {
      config.value.useCaseConfigs[newUseCase] = newModelConfigs;
    }
  }
  // always want a "Select Use Case" dropdown for adding another one
  nextTick(() => {
    if (!config.value.useCaseConfigs['']) {
      config.value.useCaseConfigs[''] = [{ provider: '', model: '' }];
    }
  });
};

const removeUseCaseConfig = (configType: 'llm' | 'embedding', useCase: string) => {
  const config = configType === 'llm' ? llmConfig : embeddingConfig;
  delete config.value.useCaseConfigs[useCase];
};

onMounted(() => {
  if (props.workspace) {
    name.value = props.workspace.name;
    localRepoDir.value = props.workspace.localRepoDir;
    configMode.value = props.workspace.configMode || 'merge';
  }
});

const filterEmptyUseCaseKeys = <T extends LLMConfig | EmbeddingConfig>(config: T): T => {
  return {
    ...config,
    useCaseConfigs: Object.fromEntries(
      Object.entries(config.useCaseConfigs)
        .filter(([key]) => key !== '')
        .map(([key, value]) => [key, [...value]])
    )
  } as T;
};

const submitWorkspace = async () => {
  const formData: Omit<Workspace, 'id'> = {
    name: name.value,
    localRepoDir: localRepoDir.value,
    configMode: configMode.value,
    llmConfig: filterEmptyUseCaseKeys(llmConfig.value),
    embeddingConfig: filterEmptyUseCaseKeys(embeddingConfig.value)
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

.config-mode-options {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.radio-option {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  cursor: pointer;
}

.radio-option input[type="radio"] {
  margin: 0;
}
</style>