<script setup lang="ts">
/**
 * Inline-спиннер. SVG, без зависимостей, наследует currentColor.
 *
 * По умолчанию — компактный (h-4 w-4) для использования внутри кнопок.
 * Через проп `size` можно сделать «hero»-вариант (например, на пустых экранах).
 */
interface Props {
  /** Размер: `sm` (16px), `md` (24px), `lg` (40px). */
  size?: 'sm' | 'md' | 'lg'
}

const props = withDefaults(defineProps<Props>(), { size: 'sm' })

const sizeClass = {
  sm: 'h-4 w-4',
  md: 'h-6 w-6',
  lg: 'h-10 w-10',
}[props.size]
</script>

<template>
  <svg
    :class="[sizeClass, 'animate-spin']"
    xmlns="http://www.w3.org/2000/svg"
    fill="none"
    viewBox="0 0 24 24"
    aria-hidden="true"
  >
    <circle
      class="opacity-20"
      cx="12"
      cy="12"
      r="10"
      stroke="currentColor"
      stroke-width="3"
    />
    <path
      class="opacity-90"
      fill="currentColor"
      d="M4 12a8 8 0 018-8v3a5 5 0 00-5 5H4z"
    />
  </svg>
</template>
