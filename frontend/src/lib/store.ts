import { reactive } from 'vue';

export const store = reactive<any>({
  workspaceId: null,
  selectWorkspaceId(workspaceId: string) {
    this.workspaceId = workspaceId;
  }
});