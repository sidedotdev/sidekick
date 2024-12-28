<template>
  <div v-if="expand && actionResult.length > 0" class="apply-edit-blocks-results">
    <div v-for="(result, index) in actionResult" :key="index" class="edit-block-result">
      <h4>
        Edit Block {{ index + 1 }} Result:
        <span v-if="result.didApply" class="result-applied">✅ Applied</span>
        <span v-else class="result-not-applied">❌ Not Applied</span>
      </h4>
      <pre v-if="result.error != ''" class="check-result-message">{{ result.error }}</pre>
      <template v-if="result.finalDiff">
        <div v-for="(parsedDiff, diffIndex) in parseFinalDiff(result.finalDiff)" :key="diffIndex" class="diff-view-container">
          <p class="file-header">{{ parsedDiff.oldFile.fileName || parsedDiff.newFile.fileName }}</p>
          <DiffView
            :data="parsedDiff"
            :diff-view-font-size="14"
            :diff-view-mode="DiffModeEnum.Unified"
            :diff-view-highlight="true"
            :diff-view-add-widget="false"
            :diff-view-wrap="true"
            :diff-view-theme="getTheme()"
          />
        </div>
      </template>
      <div v-else>
        <p>File: {{ result.originalEditBlock.filePath }}</p>
        <p>Old:</p>
        <pre>{{ result.originalEditBlock.oldLines?.join("\n") }}</pre>
        <p>New:</p>
        <pre>{{ result.originalEditBlock.newLines?.join("\n") }}</pre>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue';
import type { FlowAction } from '../lib/models';
import "@git-diff-view/vue/styles/diff-view.css";
import { DiffView, DiffModeEnum } from "@git-diff-view/vue";

// FIXME /gen switch to camelCase in backend json struct tags and here
export interface ApplyEditBlockResult {
  didApply: boolean;
  originalEditBlock: {
    filePath: string;
    oldLines: string[];
    newLines: string[];
  };
  error: string,
  checkResult?: {
    success: boolean;
    message: string;
  };
  finalDiff?: string;
}

interface ParsedDiff {
  oldFile: { fileName: string | null; fileLang: string | null };
  newFile: { fileName: string | null; fileLang: string | null };
  hunks: string[];
}

const props = defineProps({
  expand: {
    type: Boolean,
    required: true,
  },
  flowAction: {
    type: Object as () => FlowAction,
    required: true,
  },
  level: {
    type: Number,
    default: 0,
  },
});

const actionResult = computed(() => {
  let parsedResult: ApplyEditBlockResult[] = [];
  try {
    const rawResult = JSON.parse(props.flowAction.actionResult);
    if (rawResult == null) {
      return [];
    }
    parsedResult = Array.isArray(rawResult) ? rawResult : [rawResult];
  } catch (e: unknown) {
    if (props.flowAction.actionStatus != "started") {
      console.error('Failed to parse action result', e);
    }
  }
  return parsedResult;
});

const parseFinalDiff = (diffString: string): ParsedDiff[] => {
  const files = diffString.split(/^(?=diff )/);
  return files.map(file => {
    const fileHeader = file.split('\n')[0];
    const [oldFile, newFile] = fileHeader.match(/(?<=a\/).+(?= b\/)|(?<=b\/).+/g) || [];
    const hunks = [file];
    return {
      oldFile: { fileName: oldFile || null, fileLang: getFileLanguage(oldFile) },
      newFile: { fileName: newFile || null, fileLang: getFileLanguage(newFile) },
      hunks,
    };
  });
};

const getFileLanguage = (fileName: string | undefined): string | null => {
  if (!fileName) return null;
  const extension = fileName.split('.').pop();
  // Add more mappings as needed
  const languageMap: { [key: string]: string } = {
    'js': 'javascript',
    'ts': 'typescript',
    'py': 'python',
    'go': 'go',
    'vue': 'vue',
    // Add more mappings here
  };
  return languageMap[extension?.toLowerCase() ?? ''] || null;
};

const getTheme = () => {
  const prefersDarkScheme = window.matchMedia("(prefers-color-scheme: dark)");
  return prefersDarkScheme.matches ? 'dark' : 'light';
};
</script>

<style scoped>
.apply-edit-blocks-results {
  --level: v-bind(level);
  scroll-margin-top: calc(var(--level) * var(--name-height));
  background-color: inherit;
  padding: 0;
}
.edit-block-result {
  background-color: inherit;
}
.edit-block-result + .edit-block-result {
  margin-top: 1rem;
}
.result-applied {
  color: green;
  font-weight: bold;
}
.result-not-applied {
  color: red;
  font-weight: bold;
}
.check-result-message {
  margin-top: 5px;
  font-style: italic;
  padding-left: 40px;
  border-left: 2px solid #ccc;
  padding-top: 5px;
  padding-bottom: 5px;
}
.diff-view-container {
  margin: -15px;
  margin-top: 0.5rem;
  border: 1px solid var(--color-border-contrast);
  border-left: 0;
  border-right: 0;
  background-color: inherit;
}
.file-header {
  padding: 0.5rem;
  font-weight: bold;
  position: sticky;
  z-index: calc(50 - var(--level, 0));
  background-color: inherit;
  top: calc(v-bind(level) * var(--name-height) - 1px);
  border-bottom: 1px solid var(--color-border-contrast);
}

</style>