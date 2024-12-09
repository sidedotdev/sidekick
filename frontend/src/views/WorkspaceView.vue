<template>
  <div class="workspace-view">
    <h1>{{ isEditing ? 'Edit' : 'Create' }} Workspace</h1>
    <WorkspaceForm
      v-if="workspace"
      :workspace="workspace"
      @created="handleWorkspaceCreated"
      @updated="handleWorkspaceUpdated"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue';
import { useRoute, useRouter } from 'vue-router';
import WorkspaceForm from '@/components/WorkspaceForm.vue';
import type { Workspace } from '@/lib/models';

const route = useRoute();
const router = useRouter();
const workspace = ref<Workspace | undefined>(undefined);

const isEditing = computed(() => !!route.params.id);

onMounted(async () => {
  if (isEditing.value) {
    await fetchWorkspace(route.params.id as string);
  } else {
    workspace.value = {
      id: '',
      name: '',
      localRepoDir: '',
    }
  }
});

const handleWorkspaceCreated = async (newWorkspaceId: string) => {
  await fetchWorkspace(newWorkspaceId);
  router.push({ name: 'workspace', params: { id: newWorkspaceId } });
};

const handleWorkspaceUpdated = async (updatedWorkspaceId: string) => {
  await fetchWorkspace(updatedWorkspaceId);
};

const fetchWorkspace = async (workspaceId: string) => {
  try {
    const response = await fetch(`/api/v1/workspaces/${workspaceId}`);
    if (response.ok) {
      workspace.value = (await response.json()).workspace;
    } else {
      console.error('Failed to fetch workspace:', response.status);
    }
  } catch (error) {
    console.error('Error fetching workspace:', error);
  }
};
</script>

<style scoped>
.workspace-view {
  max-width: 600px;
  margin: 0 auto;
  padding: 2rem;
}
</style>