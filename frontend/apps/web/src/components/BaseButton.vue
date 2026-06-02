<script setup lang="ts">
import { computed } from 'vue'
import Spinner from '@/components/Spinner.vue'

/** Допустимые визуальные варианты кнопки. */
type Variant = 'primary' | 'secondary' | 'ghost' | 'danger'

/** Размер кнопки. */
type Size = 'sm' | 'md' | 'lg'

/** Допустимые типы (mapped 1-to-1 на native button[type]). */
type ButtonType = 'button' | 'submit' | 'reset'

/** Пропсы кнопки. */
interface Props {
  /** Визуальный стиль; по умолчанию — primary. */
  variant?: Variant
  /** Размер; по умолчанию — md. */
  size?: Size
  /** Атрибут type у <button>; по умолчанию — 'button' (НЕ submit, чтобы случайно не сабмитить формы). */
  type?: ButtonType
  /** Disabled. */
  disabled?: boolean
  /** Показывать ли спиннер вместо контента; блокирует клик. */
  loading?: boolean
  /** Растянуть на всю ширину контейнера. */
  block?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  variant: 'primary',
  size: 'md',
  type: 'button',
  disabled: false,
  loading: false,
  block: false,
})

const variantCls = computed(() => {
  switch (props.variant) {
    case 'secondary':
      return 'btn-secondary'
    case 'ghost':
      return 'btn-ghost'
    case 'danger':
      return 'btn-danger'
    default:
      return 'btn-primary'
  }
})

const sizeCls = computed(() => {
  switch (props.size) {
    case 'sm':
      return 'px-3 py-1.5 text-xs gap-1.5 rounded-lg'
    case 'lg':
      return 'px-5 py-3 text-sm gap-2.5'
    default:
      return ''
  }
})

const isDisabled = computed(() => props.disabled || props.loading)
</script>

<template>
  <button
    :type="props.type"
    :class="[variantCls, sizeCls, props.block && 'w-full']"
    :disabled="isDisabled"
    :aria-busy="props.loading"
  >
    <Spinner v-if="props.loading" />
    <template v-else>
      <slot name="icon-left" />
      <slot />
      <slot name="icon-right" />
    </template>
  </button>
</template>
