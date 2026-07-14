// Package appconfig встраивает конфиг-файлы (коэффициенты скоринга), чтобы бинарник
// был самодостаточным. Имя пакета отличается от каталога во избежание путаницы с
// internal/config (переменные окружения).
package appconfig

import _ "embed"

// ScoringYAML — содержимое config/scoring.yaml (§5 SPEC).
//
//go:embed scoring.yaml
var ScoringYAML []byte
