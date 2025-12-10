import { reactive } from 'vue';

export interface BranchInfo {
  name: string;
  isCurrent: boolean;
  isDefault: boolean;
}

interface ModelInfo {
  reasoning?: boolean;
}

interface ProviderInfo {
  models: Record<string, ModelInfo>;
}

export type ModelsData = Record<string, ProviderInfo>;

interface ModelsCache {
  data: ModelsData;
  timestamp: number;
}

const MODELS_CACHE_KEY = 'models_cache';
const MODELS_CACHE_TTL_MS = 5 * 60 * 1000; // 5 minutes

export const store = reactive<{
  workspaceId: string | null;
  selectWorkspaceId(workspaceId: string): void;
  getBranchCache(workspaceId: string | null): BranchInfo[] | null;
  setBranchCache(workspaceId: string | null, branches: BranchInfo[]): void;
  getModelsCache(): ModelsCache | null;
  setModelsCache(data: ModelsData): void;
  isModelsCacheStale(): boolean;
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
  },
  getModelsCache(): ModelsCache | null {
    const cached = sessionStorage.getItem(MODELS_CACHE_KEY);
    return cached ? JSON.parse(cached) : null;
  },
  setModelsCache(data: ModelsData) {
    const cache: ModelsCache = { data, timestamp: Date.now() };
    sessionStorage.setItem(MODELS_CACHE_KEY, JSON.stringify(cache));
  },
  isModelsCacheStale(): boolean {
    const cache = this.getModelsCache();
    if (!cache) return true;
    return Date.now() - cache.timestamp > MODELS_CACHE_TTL_MS;
  }
});