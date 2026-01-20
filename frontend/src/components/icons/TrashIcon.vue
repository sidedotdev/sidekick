<template>
  <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="20" height="20" class="trash-icon">
    <path class="filled" d="M6 7h12v13a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2V7z" stroke-width="1.5"/>
    <path class="stroked" d="M4 7h16" stroke-width="1.5" stroke-linecap="round"/>
    <path class="stroked" d="M9 7V5a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2" fill="none" stroke-width="1.5"/>
    <path class="stroked" d="M10 11v6M14 11v6" stroke-width="1.5" stroke-linecap="round"/>
  </svg>
</template>

<style scoped>
.trash-icon .filled {
  fill: white;
  stroke: black;
}

.trash-icon .stroked {
  stroke: black;
}

@media (prefers-color-scheme: dark) {
  .trash-icon .filled {
    fill: black;
    stroke: white;
  }

  .trash-icon .stroked {
    stroke: white;
  }
}
</style>