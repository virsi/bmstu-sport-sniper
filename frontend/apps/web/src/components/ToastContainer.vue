<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { useToast, type ToastKind } from '@/composables/useToast'

const { toasts, dismiss, error: pushError } = useToast()

function cls(kind: ToastKind): string {
  switch (kind) {
    case 'success':
      return 'bg-green-600 text-white'
    case 'error':
      return 'bg-red-600 text-white'
    default:
      return 'bg-gray-900 text-white'
  }
}

const items = computed(() => toasts.value)

/**
 * Слушает глобальный `api:error` (эмиттится из axios-interceptor) и поднимает toast.
 *
 * Так замыкаем цикл: client.ts не импортит useToast (избегаем циклов и testing-проблем),
 * UI-слой подписывается извне.
 */
function onApiError(ev: Event): void {
  const ce = ev as CustomEvent<{ message?: string }>
  const msg = ce.detail?.message ?? 'Ошибка запроса'
  pushError(msg)
}

onMounted(() => {
  window.addEventListener('api:error', onApiError)
})
onBeforeUnmount(() => {
  window.removeEventListener('api:error', onApiError)
})
</script>

<template>
  <div
    class="pointer-events-none fixed inset-x-0 top-4 z-50 flex flex-col items-center gap-2 px-4"
    aria-live="polite"
  >
    <div
      v-for="t in items"
      :key="t.id"
      :class="[
        'pointer-events-auto flex max-w-md items-start gap-3 rounded-md px-4 py-2 shadow-lg',
        cls(t.kind),
      ]"
      role="alert"
    >
      <p class="flex-1 text-sm">
        {{ t.message }}
      </p>
      <button
        type="button"
        class="text-white/80 hover:text-white"
        aria-label="Закрыть"
        @click="dismiss(t.id)"
      >
        ×
      </button>
    </div>
  </div>
</template>
