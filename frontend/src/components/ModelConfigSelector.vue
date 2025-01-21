<template>
  <div>
    <div v-for="(config, index) in modelValue" :key="index" class="config-item">
      <label :for="'provider' + index">Provider:</label>
      <select :id="'provider' + index" v-model="config.provider" required>
        <option value="">Select</option>
        <option v-for="provider in availableProviders" 
                :key="provider" 
                :value="provider">{{ provider }}</option>
      </select>
      <button 
        type="button" 
        @click="removeConfig(index)" 
        v-if="modelValue.length > 1" 
        class="remove-btn">Remove</button>
    </div>
    <button type="button" @click="addConfig" class="add-btn">Add Fallback</button>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { ModelConfig } from '@/lib/models';

const props = defineProps<{
  modelValue: ModelConfig[];
  type: 'llm' | 'embedding';
}>();

const emit = defineEmits<{
  (e: 'update:modelValue', value: ModelConfig[]): void;
}>();

const availableProviders = computed(() => {
  if (props.type === 'llm') {
    return ['openai', 'anthropic'];
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