import { createApp } from 'vue'
import { createPinia } from 'pinia'
import { router } from '@/router'
import App from '@/App.vue'
import '@/styles/main.css'

/**
 * Точка входа SPA.
 *
 * Порядок инициализации:
 * 1. Активируем тёмную тему на <html> (см. tailwind.config.ts darkMode: 'class').
 *    Решение по умолчанию — дарк (премиум-look, как Linear / Vercel). Если когда-то
 *    добавим toggle — переключатель будет писать класс сюда и localStorage.
 * 2. Pinia (стор должен существовать до первого `router.beforeEach`).
 * 3. Vue Router.
 * 4. Mount.
 */
document.documentElement.classList.add('dark')

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.mount('#app')
