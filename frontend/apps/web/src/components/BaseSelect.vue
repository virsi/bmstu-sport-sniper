<script setup lang="ts">
import { useId } from 'vue'

/** Один пункт в селекте. */
export interface SelectOption {
  /** Машинное значение. */
  value: string
  /** Подпись для юзера. */
  label: string
}

/** Пропсы компонента. */
interface Props {
  /** Видимый label. */
  label?: string
  /** Текущее значение (v-model). */
  modelValue?: string
  /** Список опций. */
  options: SelectOption[]
  /** Текст ошибки. */
  error?: string
  /** Подсказка под полем. */
  hint?: string
  /** Disabled. */
  disabled?: boolean
  /** Required (только подсветка/aria). */
  required?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  label: '',
  modelValue: '',
  error: '',
  hint: '',
  disabled: false,
  required: false,
})

const emit = defineEmits<{
  (e: 'update:modelValue', value: string): void
}>()

const id = useId()

function onChange(event: Event): void {
  const target = event.target as HTMLSelectElement
  emit('update:modelValue', target.value)
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
    <select
      :id="id"
      :value="props.modelValue"
      :disabled="props.disabled"
      :required="props.required"
      :aria-invalid="Boolean(props.error)"
      :aria-describedby="props.error ? `${id}-err` : props.hint ? `${id}-hint` : undefined"
      class="form-input"
      @change="onChange"
    >
      <option
        v-for="opt in props.options"
        :key="opt.value"
        :value="opt.value"
      >
        {{ opt.label }}
      </option>
    </select>
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
