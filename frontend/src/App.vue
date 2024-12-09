<script setup lang="ts">
import { RouterLink, RouterView } from 'vue-router'
import { ref, onMounted } from 'vue'
import { store } from './lib/store'
import type { Ref } from 'vue'
import type { Workspace } from './lib/models'

const workspaces: Ref<Workspace[]> = ref([])

const fetchWorkspaces = async () => {
  const response = await fetch('/api/v1/workspaces')
  const data = await response.json()
  workspaces.value = data.workspaces.sort((a: Workspace, b: Workspace) => a.name.localeCompare(b.name))
}

onMounted(() => {
  if (import.meta.env.MODE === 'development') {
    document.title = 'Sidekick Dev'
    let metaThemeColor = document.querySelector('meta[name="theme-color"]')
    if (!metaThemeColor) {
      metaThemeColor = document.createElement('meta')
    }
    metaThemeColor.setAttribute('content', '#4CAF50')
  }
  fetchWorkspaces()
  store.selectWorkspaceId(localStorage.getItem('selectedWorkspaceId'));
})

const selectedWorkspace = () => {
  console.log('selectedWorkspace', store.workspaceId)
  localStorage.setItem('selectedWorkspaceId', store.workspaceId)
}

</script>

<template>
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin="">
  <link href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:ital,wght@0,100..800;1,100..800&display=swap" rel="stylesheet">

  <div class="app-container">
    <div class="sidebar">
      <RouterLink id="logo-link" to="/kanban"><div id="logo">~</div></RouterLink>
      
      <nav class="container">
        <RouterLink to="/kanban">Board</RouterLink>
        <RouterLink to="/workspaces/new">+Space</RouterLink>
      </nav>
    </div>

    <div class="main-content">
      <header>
        <div class="container">
          <div class="workspace-selector">
            <select v-model="store.workspaceId" @change="selectedWorkspace">
              <option v-for="workspace in workspaces" :key="workspace.id" :value="workspace.id">
                {{ workspace.name }}
              </option>
            </select>
            <RouterLink :to="'/workspaces/' + store.workspaceId" class="edit-workspace-button">⚙️</RouterLink>
          </div>

          <!--RouterLink to="/kanban">Kanban</RouterLink-->
        </div>
      </header>

      <main>
        <RouterView/>
      </main>
    </div>
  </div>
</template>

<style scoped>
/* Define the grid container */
.app-container {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  grid-template-rows: auto 1fr;
  grid-template-areas: 
    "sidebar header"
    "sidebar main";
  height: 100vh;
  width: 100vw;
  --workspace-select-background-color: var(--color-background-hover);
}

@media (prefers-color-scheme: light) {
  .app-container {
    --workspace-select-background-color: var(--color-background-mute);
  }
}

/* Assign grid areas */
.sidebar {
  grid-area: sidebar;
  padding: 0 1rem;
  display: flex;
  flex-direction: column;
  background-color: var(--color-background);
  border-right: 1px solid var(--color-border);
}

header {
  grid-area: header;
  line-height: 1.5;
  max-height: 100vh;
  width: 100%;
  padding: 1rem 0;
  position: fixed;
  background-color: var(--color-background-soft);
  z-index: 1;
}

.workspace-selector {
  display: flex;
  align-items: center;
}

.workspace-selector select {
  padding: 0.2rem;
  font-size: 0.9rem;
  background-color: var(--workspace-select-background-color);
  color: var(--color-text);
  border: 1px solid var(--color-border-contrast);
  border-radius: 0.25rem;
  margin-right: 0.5rem;
}

.edit-workspace-button {
  font-size: 1.2rem;
  color: var(--color-text);
  opacity: 0.5;
  transition: opacity 0.3s ease;
  text-decoration: none;
}

.edit-workspace-button:hover {
  opacity: 1;
}

.main-content {
  grid-area: main;
  background-color: var(--color-background-soft);
  padding-left: 1rem;
  height: 100vh;
  width: 100%;
}

main {
  padding-top: 3.6rem;
  height: 100vh;
  overflow: scroll;
}

#logo-link {
  overflow: hidden;
  margin: 0 -1rem;
  padding: 0 1rem;
}


#logo {
  height: 100%;
  position: relative;
  word-spacing: -0.2em;
  letter-spacing: -0.07em;

  background:  linear-gradient(90deg, rgba(176, 78, 241, 0.7) 0%, rgba(253,29,29,0.45) 100%), #fff; 
  background-clip: text;
  -webkit-text-fill-color: transparent;
  font-size: 4rem;
  font-weight: 500;
  font-style: italic;

  background-position-x: 0%;
  padding-right: 0.6rem;
  padding-left: 3.5px;
  line-height: 0.6;
  vertical-align: middle;
}

nav {
  font-size: 12px;
  text-align: center;
}

nav a.router-link-exact-active {
  filter: brightness(1.0);
}

nav a:hover {
  background-color: rgba(255, 255, 255, 0.07)
}

@media (prefers-color-scheme: light) {
  nav a:hover {
    background-color: rgba(0, 0, 0, 0.07);
  }
  #logo-link {
    filter: brightness(1.2);
  }
  #logo-link:hover {
    filter: brightness(1.3);
  }
}

nav a {
  color: var(--color-text);
  filter: brightness(0.8);
  display: block;
  margin: 0 -1rem;
  padding: 1rem;
  border-left: 1px solid var(--color-border);
}

nav a:first-of-type {
  border: 0;
}

@media (min-width: 1024px) {
  header {
    display: flex;
    place-items: center;
    z-index: 100;
  }
  header .wrapper {
    display: flex;
    place-items: flex-start;
    flex-wrap: wrap;
  }

  nav {
    font-size: 1rem;
    padding: 0.25rem 0;
  }
}
</style>
