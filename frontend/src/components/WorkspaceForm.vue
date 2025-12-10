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
      <select v-model="configMode" class="config-mode-select">
        <option value="local">Local only</option>
        <option value="workspace">Workspace only</option>
        <option value="merge">Merge</option>
      </select>
    </div>
    <div v-show="configMode !== 'local'">
      <h3>LLMs</h3>
      <LLMConfigEditor v-model="llmConfig" />
    </div>
    <div v-show="configMode !== 'local'">
      <h3>Embeddings</h3>
      <EmbeddingConfigEditor v-model="embeddingConfig" />
    </div>
    <div class="button-container">
      <button type="submit">{{ isEditing ? 'Update' : 'Create' }} Workspace</button>
    </div>
  </form>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import LLMConfigEditor from '@/components/LLMConfigEditor.vue';
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
  display: inline-flex;
  align-items: center;
  gap: 0.5rem;
  margin: 12px 0;
  min-width: 120px;
}

.button-container {
  display: flex;
  justify-content: flex-end;
  gap: 1rem;
  margin-top: 1rem;
}

select {
  --select-background-color: var(--color-background-hover);
  padding: 0.2rem;
  font-size: 0.9rem;
  background-color: var(--select-background-color);
  color: var(--color-text);
  border: 1px solid var(--color-border-contrast);
  border-radius: 0.25rem;
  margin-right: 0.5rem;
}
</style>