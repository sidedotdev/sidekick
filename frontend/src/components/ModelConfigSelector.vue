<template>
  <div>
    <div v-if="useCaseMode" class="use-case-selector">
      <select id="useCase" v-model="selectedUseCaseValue">
        <option value="">Select Use Case</option>
        <option v-for="useCase in USE_CASES" 
                :key="useCase" 
                :value="useCase">{{ useCase }}</option>
      </select>
      <button 
        type="button" 
        @click="$emit('remove-use-case')" 
        class="remove-btn">Remove Use Case</button>
    </div>
    <template v-if="!useCaseMode || selectedUseCaseValue">
      <div v-for="(config, index) in modelValue" :key="index" class="config-item">
        <select :id="type + '-provider' + index" v-model="config.provider">
          <option value="">Select Provider</option>
          <option v-for="provider in availableProviders" 
                  :key="provider" 
                  :value="provider">{{ provider }}</option>
        </select>
        <input 
          type="text" 
          :id="type + '-model' + index" 
          v-model="config.model" 
          placeholder="Default Model"
          :required="config.provider !== '' && config.provider !== 'openai' && config.provider !== 'anthropic'"
        >
        <button 
          type="button" 
          @click="removeConfig(index)" 
          v-if="modelValue.length > 1" 
          class="remove-btn">Remove</button>
      </div>
      <button type="button" @click="addConfig" class="add-btn">Add Fallback</button>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue';
import type { ModelConfig } from '@/lib/models';

const USE_CASES = ['planning', 'coding', 'code_localization', 'judging', 'summarization', 'query_expansion'] as const;

const props = defineProps<{
  useCaseMode?: boolean;
  selectedUseCase?: string;
  modelValue: ModelConfig[];
  type: 'llm' | 'embedding';
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', value: ModelConfig[]): void;
  (e: 'update:selectedUseCase', value: string): void;
  (e: 'remove-use-case'): void;
}>();

const selectedUseCaseValue = ref(props.selectedUseCase || '')
watch(selectedUseCaseValue, (newValue) => {
  emit('update:selectedUseCase', newValue);
});

const availableProviders = computed(() => {
  if (props.type === 'llm') {
    return ['openai', 'anthropic', 'google'];
  }
  return ['openai'];
});

const addConfig = () => {
  const newConfigs = [...props.modelValue, { provider: '', model: '' }];
  emit('update:modelValue', newConfigs);
};

const removeConfig = (index: number) => {
  if (props.modelValue.length > 1) {
    const newConfigs = [...props.modelValue];
    newConfigs.splice(index, 1);
    emit('update:modelValue', newConfigs);
  }
};
</script>

<style scoped>
.use-case-selector {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-bottom: 1rem;
}

.use-case-selector select {
  flex: 1;
}

.config-item {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  margin-bottom: 0.5rem;
}

.config-item input[type="text"] {
  flex: 1;
  min-width: 8rem;
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