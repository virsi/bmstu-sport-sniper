# Multi-stage Dockerfile для Vue SPA. Этап 1 — pnpm build. Этап 2 — nginx
# с собранным dist/ и SPA-fallback на index.html.
#
# Build:
#   docker build -f frontend/apps/web/Dockerfile -t fizcultor/frontend:dev frontend

# ---------- builder ----------
FROM node:20-alpine AS builder

# Включаем pnpm через corepack (нативно в node 16+).
RUN corepack enable && corepack prepare pnpm@9 --activate

WORKDIR /src

# Сначала локфайлы (cache layer не инвалидируется при изменении кода).
COPY pnpm-workspace.yaml pnpm-lock.yaml package.json ./
COPY apps/web/package.json apps/web/
RUN pnpm install --frozen-lockfile

# Полный исходник + build.
COPY . .
RUN pnpm -C apps/web build

# ---------- runtime ----------
FROM nginx:1.27-alpine

# Custom nginx config: SPA-fallback (try_files → /index.html).
COPY apps/web/nginx.conf /etc/nginx/conf.d/default.conf

COPY --from=builder /src/apps/web/dist /usr/share/nginx/html

EXPOSE 80
