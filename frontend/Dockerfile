# syntax=docker/dockerfile:1.7
#
# Multi-stage frontend image: pnpm build → nginx-alpine static serve.
#
# Build:
#   docker build -t fizcultor/frontend:dev .
#
# Контекст сборки — frontend/.

# ---------- builder ----------
FROM node:20-alpine AS builder

WORKDIR /src

RUN corepack enable && corepack prepare pnpm@9.0.0 --activate

# Кэшируем install.
COPY pnpm-workspace.yaml pnpm-lock.yaml package.json ./
COPY apps/web/package.json apps/web/
COPY packages packages

RUN --mount=type=cache,id=pnpm,target=/root/.local/share/pnpm/store \
    pnpm install --frozen-lockfile

# Копируем исходники и собираем.
COPY . .
RUN pnpm -C apps/web build

# ---------- runtime: nginx ----------
FROM nginx:1.27-alpine

# Конфиг nginx с SPA fallback (/index.html для unknown route) и
# gzip-сжатием.
COPY <<'EOF' /etc/nginx/conf.d/default.conf
server {
    listen 80;
    server_name _;
    root /usr/share/nginx/html;
    index index.html;

    # SPA: все unknown пути отдают index.html (Vue Router resolves в браузере).
    location / {
        try_files $uri $uri/ /index.html;
    }

    # Кэширование статики (с hash в filename из vite).
    location /assets/ {
        expires 1y;
        add_header Cache-Control "public, immutable";
    }

    # gzip — для маленьких JS/CSS остаются под зрелым клиентом.
    gzip on;
    gzip_vary on;
    gzip_min_length 1024;
    gzip_types text/plain text/css application/json application/javascript text/xml application/xml application/xml+rss text/javascript;

    # Базовые security headers (Caddy перед нами уже выставляет, но если
    # frontend крутится без Caddy — заголовки тут.).
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-Frame-Options "DENY" always;
    add_header Referrer-Policy "strict-origin-when-cross-origin" always;
}
EOF

COPY --from=builder /src/apps/web/dist /usr/share/nginx/html

# nginx-image уже non-root в новых релизах, но для надёжности:
USER nginx
EXPOSE 80
