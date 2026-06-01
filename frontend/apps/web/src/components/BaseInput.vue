<script setup lang="ts">
import { useId } from 'vue'

/** Пропсы поля ввода. */
interface Props {
  /** Видимый label. */
  label?: string
  /** Тип нативного input; по умолчанию text. Поддерживает text|password|email|number|time. */
  type?: string
  /** Текущее значение (v-model). null допустим для опциональных number-полей. */
  modelValue?: string | number | null
  /** Placeholder. */
  placeholder?: string
  /** Текст ошибки (рендерится под полем). */
  error?: string
  /** Подсказка под полем (рендерится в сером, если нет error). */
  hint?: string
  /** autocomplete-атрибут (важен для логина/пароля). */
  autocomplete?: string
  /** Disabled. */
  disabled?: boolean
  /** Required (только подсветка/aria; реальная валидация — на форме). */
  required?: boolean
  /** min для number/time. */
  min?: number | string
  /** max для number/time. */
  max?: number | string
  /** step для number. */
  step?: number | string
}

const props = withDefaults(defineProps<Props>(), {
  label: '',
  type: 'text',
  modelValue: '',
  placeholder: '',
  error: '',
  hint: '',
  autocomplete: 'off',
  disabled: false,
  required: false,
  min: undefined,
  max: undefined,
  step: undefined,
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string | number | null): void
}>()

const id = useId()

/**
 * Обрабатывает input event с учётом типа: для number возвращает number
 * (или null, если пусто), для остальных — string.
 */
function onInput(event: Event): void {
  const target = event.target as HTMLInputElement
  const raw = target.value
  if (props.type === 'number') {
    if (raw === '') {
      emit('update:modelValue', null)
      return
    }
    const num = Number(raw)
    emit('update:modelValue', Number.isFinite(num) ? num : null)
    return
  }
  emit('update:modelValue', raw)
}

/** Преобразует modelValue в строку для атрибута :value. */
function toAttrValue(v: string | number | null | undefined): string {
  if (v === null || v === undefined) {
    return ''
  }
  return String(v)
}
</script>

<template>
  <div>
    <label
      v-if="props.label"
      :for="id"
      class="form-label"
    >
      {{ props.label }}
      <span
        v-if="props.required"
        class="text-red-500"
        aria-hidden="true"
      >*</span>
    </label>
    <input
      :id="id"
      :type="props.type"
      :value="toAttrValue(props.modelValue)"
      :placeholder="props.placeholder"
      :autocomplete="props.autocomplete"
      :disabled="props.disabled"
      :required="props.required"
      :min="props.min"
      :max="props.max"
      :step="props.step"
      :aria-invalid="Boolean(props.error)"
      :aria-describedby="props.error ? `${id}-err` : props.hint ? `${id}-hint` : undefined"
      class="form-input"
      @input="onInput"
    >
    <p
      v-if="props.error"
      :id="`${id}-err`"
      class="form-error"
    >
      {{ props.error }}
    </p>
    <p
      v-else-if="props.hint"
      :id="`${id}-hint`"
      class="form-hint"
    >
      {{ props.hint }}
    </p>
  </div>
</template>
