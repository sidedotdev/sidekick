import { reactive } from 'vue';

export interface BranchInfo {
  name: string;
  isCurrent: boolean;
  isDefault: boolean;
}

export const store = reactive<{
  workspaceId: string | null;
  selectWorkspaceId(workspaceId: string): void;
  getBranchCache(workspaceId: string | null): BranchInfo[] | null;
  setBranchCache(workspaceId: string | null, branches: BranchInfo[]): void;
}>({
  workspaceId: null,
  selectWorkspaceId(workspaceId: string) {
    this.workspaceId = workspaceId;
  },
  getBranchCache(workspaceId: string) {
    if (!workspaceId) return null;
    const cached = sessionStorage.getItem(`workspace_${workspaceId}_branches`);
    return cached ? JSON.parse(cached) : null;
  },
  setBranchCache(workspaceId: string, branches: BranchInfo[]) {
    if (!workspaceId) return;
    sessionStorage.setItem(`workspace_${workspaceId}_branches`, JSON.stringify(branches));
  }
});