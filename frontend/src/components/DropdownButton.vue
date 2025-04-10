<template>
  <div class="dropdown-button">
    <button 
      type="button" 
      class="main-button" 
      @click="isOpen = !isOpen"
      ref="buttonRef"
    >
      <span class="button-text">{{ primaryText }}</span>
      <span class="divider"></span>
      <span class="caret">▼</span>
    </button>
    <div v-if="isOpen" class="dropdown-menu">
      <button 
        v-for="option in options" 
        :key="option.value"
        type="button"
        class="dropdown-item"
        @click="selectOption(option)"
      >
        {{ option.label }}
      </button>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'

interface Option {
  label: string
  value: string
}

defineProps<{
  primaryText: string
  options: Option[]
}>()

const emit = defineEmits<{
  (e: 'select', value: string): void
}>()

const isOpen = ref(false)
const buttonRef = ref<HTMLElement | null>(null)

const selectOption = (option: Option) => {
  emit('select', option.value)
  isOpen.value = false
}

const handleClickOutside = (event: MouseEvent) => {
  if (buttonRef.value && !buttonRef.value.contains(event.target as Node)) {
    isOpen.value = false
  }
}

onMounted(() => {
  document.addEventListener('click', handleClickOutside)
})

onUnmounted(() => {
  document.removeEventListener('click', handleClickOutside)
})
</script>

<style scoped>
.dropdown-button {
  position: relative;
  display: inline-block;
}

.main-button {
  display: flex;
  align-items: center;
  border: none;
  border-radius: 0.25rem;
  cursor: pointer;
  transition: background-color 0.3s;
  padding: 0;
  overflow: hidden;
}

.button-text {
  padding: 0.5rem 0.75rem;
}

.divider {
  width: 1px;
  align-self: stretch;
  background-color: currentColor;
  opacity: 0.2;
}

.caret {
  padding: 0.5rem 0.75rem;
  font-size: 0.75em;
}

.dropdown-menu {
  position: absolute;
  top: 100%;
  right: 0;
  margin-top: 0.25rem;
  background-color: var(--color-background);
  border: 1px solid var(--color-border);
  border-radius: 0.25rem;
  min-width: 10rem;
  z-index: 1000;
}

.dropdown-item {
  display: block;
  width: 100%;
  padding: 0.5rem 1rem;
  text-align: left;
  border: none;
  background: none;
  color: var(--color-text);
  cursor: pointer;
}

.dropdown-item:hover {
  background-color: var(--color-background-hover);
}
</style>