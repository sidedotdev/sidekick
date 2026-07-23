<script setup lang="ts">
import { ref, onMounted } from 'vue'
import QRCode from 'qrcode'
import TrashIcon from '@/components/icons/TrashIcon.vue'

interface RemoteDevice {
  id: string
  name: string
  created: string
  lastUsed: string
}

const devices = ref<RemoteDevice[]>([])
const deviceName = ref('')
const creating = ref(false)
const error = ref<string | null>(null)
const qrCodeDataUrl = ref<string | null>(null)
const pairedDeviceName = ref<string | null>(null)

const fetchDevices = async () => {
  try {
    const response = await fetch('/api/v1/remote/pairings/')
    if (!response.ok) {
      throw new Error('Failed to fetch paired devices')
    }
    const data = await response.json()
    devices.value = data.devices ?? []
  } catch (err) {
    error.value = 'Error fetching paired devices'
    console.error(err)
  }
}

onMounted(fetchDevices)

const createPairing = async () => {
  creating.value = true
  error.value = null
  try {
    const response = await fetch('/api/v1/remote/pairings/', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: deviceName.value }),
    })
    if (!response.ok) {
      const data = await response.json().catch(() => null)
      throw new Error(data?.error ?? 'Failed to create pairing')
    }
    const data = await response.json()
    // The QR payload carries the one-time plaintext token; it cannot be
    // recovered later, so a lost code requires a new pairing.
    qrCodeDataUrl.value = await QRCode.toDataURL(
      JSON.stringify({ ticket: data.ticket, token: data.token }),
      { width: 320, margin: 2 },
    )
    pairedDeviceName.value = data.device?.name ?? deviceName.value
    deviceName.value = ''
    await fetchDevices()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to create pairing'
    console.error(err)
  } finally {
    creating.value = false
  }
}

const revokeDevice = async (device: RemoteDevice) => {
  if (!window.confirm(`Unpair device "${device.name}"?`)) return
  try {
    const response = await fetch(`/api/v1/remote/pairings/${device.id}`, {
      method: 'DELETE',
    })
    if (!response.ok) {
      throw new Error('Failed to unpair device')
    }
    devices.value = devices.value.filter(d => d.id !== device.id)
  } catch (err) {
    error.value = 'Error unpairing device'
    console.error(err)
  }
}

const formatDate = (value: string) => {
  const date = new Date(value)
  // Go serializes an unset time.Time as year 1; treat anything at or before
  // the epoch as "never".
  return Number.isNaN(date.getTime()) || date.getTime() <= 0 ? 'never' : date.toLocaleString()
}
</script>

<template>
  <div class="remote-control">
    <h2>Remote Control</h2>
    <p class="description">
      Pair the Sidekick mobile app to control this server remotely. Generate a
      pairing code below, then scan it from the app.
    </p>

    <form class="pairing-form" @submit.prevent="createPairing">
      <input
        v-model="deviceName"
        type="text"
        placeholder="Device name (e.g. My Phone)"
        aria-label="Device name"
      />
      <button type="submit" class="generate-button" :disabled="creating">
        Generate pairing code
      </button>
    </form>

    <p v-if="error" class="error">{{ error }}</p>

    <div v-if="qrCodeDataUrl" class="qr-section">
      <img :src="qrCodeDataUrl" :alt="`Pairing QR code for ${pairedDeviceName}`" class="qr-code" />
      <p class="qr-note">
        Scan this code with the Sidekick app. It contains a one-time token that
        is never shown again, so generate a new pairing code if this one is
        lost before scanning.
      </p>
    </div>

    <h3>Paired devices</h3>
    <p v-if="devices.length === 0" class="empty">No devices are paired yet.</p>
    <ul v-else class="device-list">
      <li v-for="device in devices" :key="device.id" class="device">
        <div class="device-info">
          <span class="device-name">{{ device.name }}</span>
          <span class="device-meta">
            Paired {{ formatDate(device.created) }} · Last used {{ formatDate(device.lastUsed) }}
          </span>
        </div>
        <button
          class="revoke-button"
          :aria-label="`Unpair ${device.name}`"
          @click="revokeDevice(device)"
        >
          <TrashIcon />
        </button>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.remote-control {
  max-width: 40rem;
  padding: 1.5rem;
}

.description {
  color: var(--color-text-2);
  margin: 0.5rem 0 1.5rem;
}

.pairing-form {
  display: flex;
  gap: 0.75rem;
  margin-bottom: 1.5rem;
}

.pairing-form input {
  flex: 1;
  padding: 0.5em 0.75em;
  border: 1px solid var(--color-border-contrast);
  border-radius: 0.375rem;
  background-color: var(--color-background-soft);
  color: var(--color-text);
}

.generate-button {
  padding: 0.5em 1em;
  border: none;
  border-radius: 0.375rem;
  background-color: var(--color-cta-button-bg);
  color: var(--color-cta-button-text);
  cursor: pointer;
}

.generate-button:disabled {
  opacity: 0.6;
  cursor: default;
}

.error {
  color: var(--color-error-text);
  background-color: var(--color-error-background);
  border: 1px solid var(--color-error-border);
  border-radius: 0.375rem;
  padding: 0.5em 0.75em;
  margin-bottom: 1.5rem;
}

.qr-section {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.75rem;
  margin-bottom: 1.5rem;
}

.qr-code {
  /* White quiet zone keeps the code scannable regardless of theme. */
  background-color: white;
  border-radius: 0.5rem;
  width: 20rem;
  max-width: 100%;
}

.qr-note {
  color: var(--color-text-2);
  text-align: center;
}

.empty {
  color: var(--color-text-2);
}

.device-list {
  list-style: none;
  padding: 0;
  margin: 0.5rem 0 0;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.device {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 0.75rem;
  padding: 0.75em 1em;
  border: 1px solid var(--color-border);
  border-radius: 0.5rem;
  background-color: var(--color-background-soft);
}

.device-info {
  display: flex;
  flex-direction: column;
  gap: 0.125rem;
}

.device-name {
  font-weight: 600;
}

.device-meta {
  color: var(--color-text-2);
  font-size: 0.85em;
}

.revoke-button {
  border: none;
  background: none;
  color: var(--color-text-2);
  cursor: pointer;
  padding: 0.25em;
  display: flex;
  align-items: center;
}

.revoke-button:hover {
  color: var(--color-text);
}
</style>