<template>
  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" :width="size" :height="size">
    <!-- Back rectangle (filled) -->
    <rect x="8" y="8" width="12" height="12" rx="2" :fill="color" stroke="currentColor" stroke-width="1.5"/>
    
    <!-- Front rectangle (outlined) -->
    <rect x="4" y="4" width="12" height="12" rx="2" :fill="color" stroke="currentColor" stroke-width="1.5"/>
  </svg>
</template>

<script setup lang="ts">
interface Props {
  size?: string | number
  color?: string
}

withDefaults(defineProps<Props>(), {
  size: '1em',
  color: 'currentColor'
})
</script>