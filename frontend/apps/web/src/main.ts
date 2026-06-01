import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { router } from '@/router'
import App from '@/App.vue'
import '@/styles/main.css'

/**
 * Точка входа SPA.
 *
 * Порядок инициализации:
 * 1. Pinia (стор должен существовать до первого `router.beforeEach`).
 * 2. Vue Router.
 * 3. Mount.
 */
const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')
