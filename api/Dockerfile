# --- сборка ---
# Базовый слой — с зеркала Google (mirror.gcr.io), не Docker Hub: анонимные пуллы
# с Docker Hub на VPS регулярно ловят 429 Too Many Requests при пересборке (см.
# инцидент 04.08.2026), а mirror.gcr.io отдаёт тот же образ без этого лимита.
# Рантайм ниже уже на gcr.io/distroless — там менять нечего.
FROM mirror.gcr.io/library/golang:1.25-alpine AS build
WORKDIR /src

# Зависимости вендорятся (api/vendor) — сборка образа идёт без обращения к сети.
COPY . .
# Пустой каталог под шер-карточки: в distroless нет shell, а mkdir на рантайме упрётся
# в права — контейнер работает от nonroot (65532), и том, созданный Docker'ом пустым,
# наследует владельца из ОБРАЗА. Поэтому каталог с нужным владельцем готовим здесь.
RUN mkdir -p /out/share
RUN CGO_ENABLED=0 GOFLAGS="-trimpath -mod=vendor" go build -ldflags="-s -w" -o /out/api      ./cmd/api \
 && CGO_ENABLED=0 GOFLAGS="-trimpath -mod=vendor" go build -ldflags="-s -w" -o /out/migrate  ./cmd/migrate \
 && CGO_ENABLED=0 GOFLAGS="-trimpath -mod=vendor" go build -ldflags="-s -w" -o /out/seed     ./cmd/seed \
 && CGO_ENABLED=0 GOFLAGS="-trimpath -mod=vendor" go build -ldflags="-s -w" -o /out/evalpipe ./cmd/evalpipe

# --- рантайм (distroless: без shell/пакетного менеджера, ca-certificates внутри) ---
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/api      /usr/local/bin/api
COPY --from=build /out/migrate  /usr/local/bin/migrate
COPY --from=build /out/seed     /usr/local/bin/seed
COPY --from=build /out/evalpipe /usr/local/bin/evalpipe
# Каталог шер-карточек (SHARE_DIR). Владелец — nonroot, иначе запись в примонтированный
# том падает с permission denied: том наследует права отсюда при первом создании.
COPY --from=build --chown=65532:65532 /out/share /data/share
EXPOSE 8080
# Healthcheck выполняется самим бинарником: `api healthcheck` (в distroless нет wget).
ENTRYPOINT ["/usr/local/bin/api"]
