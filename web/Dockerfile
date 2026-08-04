# Web-контейнер раздаёт готовую статику mini-app. Сборку SPA делает хост/CI
# (`make web-build` → dist/): контейнеру node не нужен, образ — только nginx.
# Базовый слой — с зеркала Google (mirror.gcr.io), не Docker Hub: анонимные пуллы
# с Docker Hub на VPS регулярно ловят 429 Too Many Requests при пересборке (см.
# инцидент 04.08.2026), а mirror.gcr.io отдаёт тот же образ без этого лимита.
FROM mirror.gcr.io/library/nginx:1.27-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY dist /usr/share/nginx/html
EXPOSE 80
