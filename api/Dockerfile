# --- сборка ---
FROM golang:1.25-alpine AS build
WORKDIR /src

# Зависимости вендорятся (api/vendor) — сборка образа идёт без обращения к сети.
COPY . .
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
EXPOSE 8080
# Healthcheck выполняется самим бинарником: `api healthcheck` (в distroless нет wget).
ENTRYPOINT ["/usr/local/bin/api"]
