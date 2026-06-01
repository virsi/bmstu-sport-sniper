<script setup lang="ts">
import { computed } from 'vue'
import Spinner from '@/components/Spinner.vue'

/** Допустимые визуальные варианты кнопки. */
type Variant = 'primary' | 'secondary' | 'danger'

/** Допустимые типы (mapped 1-to-1 на native button[type]). */
type ButtonType = 'button' | 'submit' | 'reset'

/** Пропсы кнопки. */
interface Props {
  /** Визуальный стиль; по умолчанию — primary. */
  variant?: Variant
  /** Атрибут type у <button>; по умолчанию — 'button' (НЕ submit, чтобы не сабмитить формы случайно). */
  type?: ButtonType
  /** Disabled. */
  disabled?: boolean
  /** Показывать ли спиннер вместо контента; блокирует клик. */
  loading?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  variant: 'primary',
  type: 'button',
  disabled: false,
  loading: false,
})

const cls = computed(() => {
  switch (props.variant) {
    case 'secondary':
      return 'btn-secondary'
    case 'danger':
      return 'btn-danger'
    default:
      return 'btn-primary'
  }
})

const isDisabled = computed(() => props.disabled || props.loading)
</script>

<template>
  <button
    :type="props.type"
    :class="cls"
    :disabled="isDisabled"
    :aria-busy="props.loading"
  >
    <Spinner v-if="props.loading" />
    <slot v-else />
  </button>
</template>
