<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { isBlockedNow, clearOffHoursCache, type OffHoursStatus } from '@/lib/offHours'

const route = useRoute()
const router = useRouter()

const status = ref<OffHoursStatus>({ blocked: true, unblockAt: null, message: 'Time to rest!' })
const checkInterval = ref<ReturnType<typeof setInterval> | null>(null)

const redirectPath = computed(() => {
  const redirect = route.query.redirect
  if (typeof redirect === 'string' && redirect) {
    return redirect
  }
  return '/kanban'
})

const formattedUnblockTime = computed(() => {
  if (!status.value.unblockAt) return null
  return status.value.unblockAt.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
})

const checkBlocked = async () => {
  clearOffHoursCache()
  const result = await isBlockedNow()
  status.value = result

  if (!result.blocked) {
    router.replace(redirectPath.value)
  }
}

onMounted(() => {
  checkBlocked()
  checkInterval.value = setInterval(checkBlocked, 10000)
})

onUnmounted(() => {
  if (checkInterval.value) {
    clearInterval(checkInterval.value)
  }
})
</script>

<template>
  <div class="blocked-container">
    <div class="blocked-content">
      <div class="blocked-icon">🌙</div>
      <h1 class="blocked-title">{{ status.message }}</h1>
      <p v-if="formattedUnblockTime" class="blocked-subtitle">
        Access will resume at {{ formattedUnblockTime }}
      </p>
      <p class="blocked-hint">This page will automatically redirect when the block ends.</p>
    </div>
  </div>
</template>

<style scoped>
.blocked-container {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 100vh;
  padding: 2rem;
  background: var(--color-background);
}

.blocked-content {
  text-align: center;
  max-width: 30rem;
}

.blocked-icon {
  font-size: 4rem;
  margin-bottom: 1.5rem;
}

.blocked-title {
  font-size: 2rem;
  font-weight: 600;
  color: var(--color-heading);
  margin-bottom: 1rem;
}

.blocked-subtitle {
  font-size: 1.25rem;
  color: var(--color-text);
  margin-bottom: 1.5rem;
}

.blocked-hint {
  font-size: 0.875rem;
  color: var(--color-text-mute);
}
</style>