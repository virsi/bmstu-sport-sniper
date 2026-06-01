<script setup lang="ts">
import { ref } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { useAuthStore } from '@/stores/auth'
import { useToast } from '@/composables/useToast'
import { extractErrorMessage } from '@/api/client'
import BaseButton from '@/components/BaseButton.vue'
import BaseInput from '@/components/BaseInput.vue'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()
const { error: toastError } = useToast()

const schema = toTypedSchema(
  z.object({
    email: z.string().email('Неверный формат email'),
    password: z.string().min(8, 'Минимум 8 символов'),
  }),
)

const { defineField, handleSubmit, errors } = useForm({ validationSchema: schema })
const [email, emailAttrs] = defineField('email')
const [password, passwordAttrs] = defineField('password')
const submitting = ref(false)

const onSubmit = handleSubmit(async (values) => {
  submitting.value = true
  try {
    await auth.login(values.email, values.password)
    const target = (route.query.redirect as string | undefined) ?? '/'
    await router.push(target)
  } catch (err) {
    toastError(extractErrorMessage(err))
  } finally {
    submitting.value = false
  }
})
</script>

<template>
  <main class="flex min-h-full items-center justify-center px-4 py-12">
    <div class="w-full max-w-sm">
      <h1 class="mb-6 text-center text-2xl font-bold text-gray-900">
        Вход
      </h1>
      <form
        class="space-y-4"
        novalidate
        @submit="onSubmit"
      >
        <BaseInput
          v-model="email"
          v-bind="emailAttrs"
          label="Email"
          type="email"
          autocomplete="email"
          required
          :error="errors.email"
        />
        <BaseInput
          v-model="password"
          v-bind="passwordAttrs"
          label="Пароль"
          type="password"
          autocomplete="current-password"
          required
          :error="errors.password"
        />
        <BaseButton
          type="submit"
          :loading="submitting"
          class="w-full"
        >
          Войти
        </BaseButton>
      </form>
      <p class="mt-4 text-center text-sm text-gray-600">
        Нет аккаунта?
        <RouterLink
          to="/register"
          class="font-medium text-brand-600 hover:text-brand-700"
        >
          Регистрация
        </RouterLink>
      </p>
    </div>
  </main>
</template>
