import './assets/main.css'

import { createApp } from 'vue'
import PrimeVue from 'primevue/config'
import { definePreset } from '@primeuix/themes';
import Aura from '@primeuix/themes/aura';
import App from './App.vue'
import router from './router'
import { isBlockedNow, clearOffHoursCache } from './lib/offHours'

const app = createApp(App)

const MyPreset = definePreset(Aura, {
    semantic: {
        primary: {
            50: '{purple.50}',
            100: '{purple.100}',
            200: '{purple.200}',
            300: '{purple.300}',
            400: '{purple.400}',
            500: '{purple.500}',
            600: '{purple.600}',
            700: '{purple.700}',
            800: '{purple.800}',
            900: '{purple.900}',
            950: '{purple.950}'
        }
    }
});

app.use(PrimeVue, {
    theme: {
        preset: MyPreset,
        options: {
            darkModeSelector: '.dark-mode',
        }
    }
});
app.use(router)

app.mount('#app')

setInterval(async () => {
  if (router.currentRoute.value.name === 'blocked') {
    return
  }
  clearOffHoursCache()
  const status = await isBlockedNow()
  if (status.blocked) {
    router.push({
      name: 'blocked',
      query: { redirect: router.currentRoute.value.fullPath },
    })
  }
}, 30000)
