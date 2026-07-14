# Web-контейнер раздаёт готовую статику mini-app. Сборку SPA делает хост/CI
# (`make web-build` → dist/): контейнеру node не нужен, образ — только nginx.
FROM nginx:1.27-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY dist /usr/share/nginx/html
EXPOSE 80
