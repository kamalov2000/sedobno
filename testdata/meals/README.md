# testdata/meals — фото еды для калибровки модуля калорий

Сюда кладём **реальные фотографии готовой еды на тарелке** (JPEG/PNG/WebP) для
тест-прогона модуля «Счётчик калорий по фото» (feature/calories). Часть — с известным
весом порции (взвесили) — пометки в `ground_truth.md`. Полезно добавить и негативы:
фото упаковки с этикеткой (ожидаем `label_detected`) и пустой тарелки (ожидаем `not_food`).

## Как запустить прогон

Нужен реальный `ANTHROPIC_API_KEY` (прогон делает платные vision-вызовы). Запуск —
через live-тег, чтобы не гонялся в CI:

```bash
# из корня репозитория, с загруженным .env (ANTHROPIC_API_KEY)
make meal-eval
# или напрямую:
set -a; . ./.env; set +a
cd api && go test -tags live ./internal/meal/ -run TestMealEval -v -timeout 30m
```

Прогон делается для двух моделей (Haiku по умолчанию и Sonnet через MEAL_VISION_MODEL)
и пишет рядом:
- `results_haiku.json`, `results_sonnet.json` — сырые результаты (блюда, граммовки, КБЖУ,
  confidence, latency, cost) по каждому фото;
- `results_report.md` — сравнительный отчёт: таблица по фото, расхождение граммовок с
  ground truth, confidence, среднее время, суммарная и поштучная стоимость в рублях,
  проекция себестоимости при 3 разборах/день.

## Переменные (опционально)

- `MEAL_EVAL_DIR` — папка с фото (по умолчанию `testdata/meals`).
- `MEAL_EVAL_MODELS` — список `имя=модель` через запятую
  (по умолчанию `haiku=claude-haiku-4-5,sonnet=claude-sonnet-5`).
- `LLM_USD_RUB` — курс для оценки стоимости (по умолчанию 95).
