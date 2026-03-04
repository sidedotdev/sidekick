<template>
  <div class="image-preview">
    <img
      :src="src"
      class="image-thumbnail"
      @click="showModal = true"
      alt="Tool result image"
    />
    <Teleport to="body">
      <div v-if="showModal" class="image-modal-overlay" @click="showModal = false">
        <div class="image-modal-content" @click.stop>
          <button class="image-modal-close" @click="showModal = false">&times;</button>
          <img :src="src" class="image-full" alt="Tool result image" />
        </div>
      </div>
    </Teleport>
  </div>
</template>

<script setup lang="ts">
import { ref } from 'vue'

defineProps<{
  src: string
}>()

const showModal = ref(false)
</script>

<style scoped>
.image-preview {
  display: inline-block;
  margin: 0.5em 0;
}

.image-thumbnail {
  max-width: 20rem;
  max-height: 15rem;
  object-fit: contain;
  border-radius: 0.25rem;
  border: 1px solid var(--color-border);
  cursor: pointer;
  transition: opacity 0.15s ease;
}

.image-thumbnail:hover {
  opacity: 0.85;
}

.image-modal-overlay {
  position: fixed;
  inset: 0;
  z-index: 9999;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.75);
}

.image-modal-content {
  position: relative;
  max-width: 90vw;
  max-height: 90vh;
}

.image-modal-close {
  position: absolute;
  top: -2rem;
  right: -0.5rem;
  background: none;
  border: none;
  color: white;
  font-size: 1.75rem;
  cursor: pointer;
  line-height: 1;
  padding: 0.25rem;
}

.image-modal-close:hover {
  opacity: 0.7;
}

.image-full {
  max-width: 90vw;
  max-height: 90vh;
  object-fit: contain;
  border-radius: 0.25rem;
}
</style>