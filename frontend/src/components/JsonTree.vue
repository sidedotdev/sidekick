<template>
  <VueJsonPretty :data="jsonData" :deep="deep" :show-double-quotes="false" :show-icon="true" :show-line="false" :theme="theme"/>
</template>

<script setup lang="ts">
import { ref, onMounted, type PropType } from 'vue'
import VueJsonPretty from 'vue-json-pretty'
import 'vue-json-pretty/lib/styles.css'
import type { JSONDataType } from 'vue-json-pretty/types/utils';


const props = defineProps({
  data: {
    type: Object as PropType<Object|JSONDataType>,
    required: true,
  },
  deep : {
    type: Number,
    default: 3,
  },
})

const jsonData = props.data as JSONDataType

const theme = ref<'dark' | 'light'>('dark')

onMounted(() => {
  if (window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches) {
    theme.value = 'light'
  } else {
    theme.value = 'dark'
  }
})
</script>

<style scoped>
:deep(.vjs-tree) {
  font-size: 14px;
  background-color: #222;
  padding: 5px;
  border-radius: 4px;
}

:deep(.vjs-tree .vjs-value__string) {
  color: #a8ff60;
}

:deep(.vjs-tree .vjs-value__number) {
  color: #ff9d00;
}

:deep(.vjs-tree .vjs-value__boolean) {
  color: #ff628c;
}

:deep(.vjs-tree .vjs-value__null) {
  color: #ff628c;
}

:deep(.vjs-tree .vjs-key) {
  color: #5ccfe6;
}
</style>