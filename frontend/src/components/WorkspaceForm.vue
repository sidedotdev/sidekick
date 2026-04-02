<template>
  <form @submit.prevent="submitWorkspace">
    <div>
      <label for="name">Name</label>
      <input id="name" v-model="name" required placeholder="Workspace name">
    </div>
    <div>
      <label for="localRepoDir">Repository Directory</label>
      <input id="localRepoDir" v-model="localRepoDir" required placeholder="Path to local repository">
    </div>
    <div>
      <label>Configuration Mode</label>
      <select v-model="configMode">
        <option value="local">Local only</option>
        <option value="workspace">Workspace only</option>
        <option value="merge">Merge</option>
      </select>
    </div>
    <div v-show="configMode !== 'local'">
      <label>LLMs</label>
      <LlmConfigEditor v-model="llmConfig" />
    </div>
    <div v-show="configMode !== 'local'">
      <label>Embeddings</label>
      <EmbeddingConfigEditor v-model="embeddingConfig" />
    </div>
    <div class="button-container">
      <button type="submit" class="submit-button">{{ isEditing ? 'Update' : 'Create' }} Workspace</button>
    </div>
  </form>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import LlmConfigEditor from '@/components/LlmConfigEditor.vue';
import EmbeddingConfigEditor from '@/components/EmbeddingConfigEditor.vue';
import type { Workspace, LLMConfig, EmbeddingConfig } from '@/lib/models';

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
const llmConfig = ref<LLMConfig>({ 
  defaults: props.workspace.llmConfig?.defaults?.length ? [...props.workspace.llmConfig.defaults] : [{ provider: '', model: '' }], 
  useCaseConfigs: props.workspace.llmConfig?.useCaseConfigs ? { ...props.workspace.llmConfig.useCaseConfigs } : {}
});
const embeddingConfig = ref<EmbeddingConfig>({ 
  defaults: props.workspace.embeddingConfig?.defaults?.length ? [...props.workspace.embeddingConfig.defaults] : [{ provider: '', model: '' }], 
  useCaseConfigs: {}
});

const isEditing = computed(() => !!props.workspace.id);

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
}

form > div {
  margin-top: 0.5rem;
}

label {
  display: block;
  margin: 0.75rem 0 0.375rem 0;
  font-size: 0.875rem;
  color: var(--color-text-2);
}

input[type="text"],
input:not([type]) {
  width: 100%;
  padding: 0.5rem;
  font-size: 0.9rem;
  background-color: var(--color-background);
  color: var(--color-text);
  border: 1px solid var(--color-border-contrast);
  border-radius: 0.25rem;
  box-sizing: border-box;
}

input[type="text"]:focus,
input:not([type]):focus {
  outline: none;
  border-color: var(--color-primary);
}

select {
  padding: 0.5rem;
  font-size: 0.9rem;
  background-color: var(--color-background);
  color: var(--color-text);
  border: 1px solid var(--color-border-contrast);
  border-radius: 0.25rem;
}

.button-container {
  display: flex;
  justify-content: flex-start;
  margin-top: 1.5rem;
}

.submit-button {
  padding: 0.5rem 1.25rem;
  font-size: 0.9rem;
  background-color: var(--color-cta-button-bg);
  color: var(--color-cta-button-text);
  border: none;
  border-radius: 0.25rem;
  cursor: pointer;
}
</style>