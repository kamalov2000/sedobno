// Package seeddata встраивает сид словаря добавок в бинарник, чтобы команда seed
// не зависела от внешних файлов (аналогично migrations).
package seeddata

import _ "embed"

// AdditivesJSON — содержимое data/additives_seed.json (§6 SPEC).
//
//go:embed additives_seed.json
var AdditivesJSON []byte
