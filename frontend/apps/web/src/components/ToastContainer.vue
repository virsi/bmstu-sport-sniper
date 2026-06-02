<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { CheckCircle2, AlertTriangle, Info, X } from 'lucide-vue-next'
import { useToast, type ToastKind } from '@/composables/useToast'

const { toasts, dismiss, error: pushError } = useToast()

/**
 * Возвращает [bg-класс, border-класс, icon-component] для типа тоста.
 *
 * Цвета — приглушённые тёмные surfaces с цветной рамкой/иконкой; на dark-теме
 * это выглядит «продуктово», без кричащих заливок (как у Linear / Vercel).
 */
function styleFor(kind: ToastKind): {
  wrap: string
  iconCls: string
  icon: typeof CheckCircle2
} {
  switch (kind) {
    case 'success':
      return {
        wrap: 'border-emerald-500/30 bg-emerald-500/[0.06]',
        iconCls: 'text-emerald-400',
        icon: CheckCircle2,
      }
    case 'error':
      return {
        wrap: 'border-rose-500/30 bg-rose-500/[0.06]',
        iconCls: 'text-rose-400',
        icon: AlertTriangle,
      }
    default:
      return {
        wrap: 'border-zinc-700 bg-zinc-900/80',
        iconCls: 'text-zinc-300',
        icon: Info,
      }
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
    class="pointer-events-none fixed inset-x-0 top-4 z-50 flex flex-col items-center gap-2 px-4 sm:items-end sm:px-6"
    aria-live="polite"
  >
    <TransitionGroup
      enter-active-class="transition-all duration-300 ease-out"
      enter-from-class="translate-x-4 opacity-0 sm:translate-x-10"
      enter-to-class="translate-x-0 opacity-100"
      leave-active-class="transition-all duration-200 ease-in"
      leave-from-class="opacity-100"
      leave-to-class="-translate-y-2 opacity-0 sm:translate-x-10 sm:translate-y-0"
      tag="div"
      class="flex w-full max-w-md flex-col gap-2"
    >
      <div
        v-for="t in items"
        :key="t.id"
        :class="[
          'pointer-events-auto flex items-start gap-3 rounded-2xl border px-4 py-3 shadow-elevated backdrop-blur-xl',
          styleFor(t.kind).wrap,
        ]"
        role="alert"
      >
        <component
          :is="styleFor(t.kind).icon"
          :class="['mt-0.5 h-5 w-5 shrink-0', styleFor(t.kind).iconCls]"
          aria-hidden="true"
        />
        <p class="flex-1 text-sm leading-snug text-zinc-100">
          {{ t.message }}
        </p>
        <button
          type="button"
          class="-m-1 rounded-md p-1 text-zinc-400 transition-colors hover:bg-zinc-800/50 hover:text-zinc-100"
          aria-label="Закрыть"
          @click="dismiss(t.id)"
        >
          <X class="h-4 w-4" />
        </button>
      </div>
    </TransitionGroup>
  </div>
</template>
