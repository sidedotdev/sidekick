<template>
  <svg xmlns="http://www.w3.org/2000/svg" viewBox="3.25 3.25 17.5 17.5" width="20" height="20" class="copy-icon">
    <path d="M16 8 L18 8 A2 2 0 0 1 20 10 L20 18 A2 2 0 0 1 18 20 L10 20 A2 2 0 0 1 8 18 L8 16" stroke-width="1.5"/>
    <rect x="4" y="4" width="12" height="12" rx="2" stroke-width="1.5"/>
  </svg>
</template>

<style scoped>
.copy-icon rect,
.copy-icon path {
  fill: transparent;
  stroke: black;
}

@media (prefers-color-scheme: dark) {
  .copy-icon rect,
  .copy-icon path {
    fill: transparent;
    stroke: white;
  }
}
</style>