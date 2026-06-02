<script setup lang="ts">
import { ref } from 'vue'
import { useRouter, useRoute, RouterLink } from 'vue-router'
import { useForm } from 'vee-validate'
import { toTypedSchema } from '@vee-validate/zod'
import { z } from 'zod'
import { Mail, Lock, Zap, Shield, Sparkles, Users } from 'lucide-vue-next'
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
    password: z.string().min(4, 'Минимум 4 символа'),
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
  <main class="flex min-h-full">
    <!-- ===== Brand hero (desktop only ≥ lg) ===== -->
    <aside
      class="relative hidden flex-1 flex-col justify-between overflow-hidden border-r border-zinc-800/80 bg-zinc-950 p-10 lg:flex"
    >
      <!-- Декоративный градиент -->
      <div
        class="pointer-events-none absolute inset-0 bg-gradient-hero opacity-90"
        aria-hidden="true"
      />
      <div
        class="pointer-events-none absolute inset-0 bg-grid-zinc opacity-30"
        aria-hidden="true"
      />

      <RouterLink
        to="/"
        class="relative z-10 flex items-center gap-3"
      >
        <span class="flex h-10 w-10 items-center justify-center rounded-xl bg-gradient-brand shadow-glow">
          <Zap class="h-6 w-6 text-white" />
        </span>
        <span class="text-lg font-bold tracking-tight text-zinc-50">
          fizcultor
        </span>
      </RouterLink>

      <div class="relative z-10 max-w-md">
        <span class="badge-violet mb-6">
          <Sparkles class="h-3 w-3" />
          BMSTU · LKS live
        </span>
        <h1 class="h-display mb-4">
          Снайпер слотов
          <span class="bg-gradient-to-br from-brand-300 via-brand-400 to-emerald-200 bg-clip-text text-transparent">
            BMSTU
          </span>
        </h1>
        <p class="text-lg leading-relaxed text-zinc-400">
          Для тех, кто не хочет F5-ить расписание физкультуры.
          Настрой фильтр — алёрт прилетит в Telegram через
          <span class="font-semibold text-zinc-200">3 секунды</span>
          после появления места.
        </p>

        <div class="mt-10 grid grid-cols-2 gap-4">
          <div class="rounded-2xl border border-zinc-800/80 bg-zinc-900/40 p-4 backdrop-blur-md">
            <Users class="mb-2 h-5 w-5 text-brand-400" />
            <p class="text-2xl font-bold tracking-tight tabular text-zinc-50">
              50+
            </p>
            <p class="text-xs text-zinc-500">
              студентов подписаны
            </p>
          </div>
          <div class="rounded-2xl border border-zinc-800/80 bg-zinc-900/40 p-4 backdrop-blur-md">
            <Shield class="mb-2 h-5 w-5 text-accent-400" />
            <p class="text-2xl font-bold tracking-tight tabular text-zinc-50">
              AES-256
            </p>
            <p class="text-xs text-zinc-500">
              шифрование кредов
            </p>
          </div>
        </div>
      </div>

      <p class="relative z-10 font-mono text-2xs text-zinc-600">
        v0.1 · Wave-3 release
      </p>
    </aside>

    <!-- ===== Login form ===== -->
    <section class="flex flex-1 items-center justify-center px-4 py-12 sm:px-8">
      <div class="w-full max-w-sm">
        <!-- Mobile-only компактное лого -->
        <div class="mb-8 flex flex-col items-center text-center lg:hidden">
          <span class="mb-3 flex h-12 w-12 items-center justify-center rounded-2xl bg-gradient-brand shadow-glow">
            <Zap class="h-6 w-6 text-white" />
          </span>
          <h2 class="text-2xl font-bold tracking-tight text-zinc-50">
            fizcultor
          </h2>
          <p class="mt-1 text-xs text-zinc-500">
            Снайпер слотов BMSTU
          </p>
        </div>

        <div class="mb-8 hidden lg:block">
          <h2 class="text-2xl font-bold tracking-tight text-zinc-50">
            С возвращением
          </h2>
          <p class="mt-1.5 text-sm text-zinc-400">
            Войди в аккаунт, чтобы продолжить.
          </p>
        </div>

        <form
          class="space-y-5"
          novalidate
          @submit="onSubmit"
        >
          <BaseInput
            v-model="email"
            v-bind="emailAttrs"
            label="Email"
            type="email"
            placeholder="you@bmstu.ru"
            autocomplete="email"
            required
            :error="errors.email"
          >
            <template #icon-left>
              <Mail class="h-4 w-4" />
            </template>
          </BaseInput>
          <BaseInput
            v-model="password"
            v-bind="passwordAttrs"
            label="Пароль"
            type="password"
            placeholder="••••••••"
            autocomplete="current-password"
            required
            :error="errors.password"
          >
            <template #icon-left>
              <Lock class="h-4 w-4" />
            </template>
          </BaseInput>
          <BaseButton
            type="submit"
            :loading="submitting"
            block
            size="lg"
          >
            Войти
          </BaseButton>
        </form>

        <p class="mt-6 text-center text-sm text-zinc-500">
          Нет аккаунта?
          <RouterLink
            to="/register"
            class="font-semibold text-brand-400 transition-colors hover:text-brand-300"
          >
            Регистрация →
          </RouterLink>
        </p>
      </div>
    </section>
  </main>
</template>
