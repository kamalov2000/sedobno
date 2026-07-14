// Команда seed загружает встроенный словарь добавок (data/additives_seed.json)
// в таблицу additives. Идемпотентна (upsert по code). Запускается после миграций.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	seeddata "github.com/sostav/api/data"
	"github.com/sostav/api/internal/config"
	"github.com/sostav/api/internal/db"
	"github.com/sostav/api/internal/dict"
)

func main() {
	cfg := config.MustLoad()

	items, err := dict.LoadSeed(seeddata.AdditivesJSON)
	if err != nil {
		fail("разбор сида", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		fail("подключение к БД", err)
	}
	defer pool.Close()

	n, err := dict.Upsert(ctx, pool, items)
	if err != nil {
		fail("загрузка словаря", err)
	}

	fmt.Printf("seed: загружено записей словаря: %d\n", n)
}

func fail(what string, err error) {
	fmt.Fprintf(os.Stderr, "seed: %s: %v\n", what, err)
	os.Exit(1)
}
