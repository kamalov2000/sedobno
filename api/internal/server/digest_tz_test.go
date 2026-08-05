package server

import "testing"

// Человек ест примерно в одни и те же локальные часы; проверяем, что по UTC-меткам
// восстанавливается его пояс. Владивосток — главный случай: именно на нём наивная
// (нециклическая) медиана давала +4 вместо +10 и слала итог в 6 утра.
func TestGuessUTCOffset_ByCity(t *testing.T) {
	// localHours → часы в UTC при заданном смещении.
	toUTC := func(local []int, off int) []int {
		out := make([]int, 0, len(local))
		for _, h := range local {
			u := (h - off + 24) % 24
			out = append(out, u)
		}
		return out
	}
	local := []int{8, 9, 13, 14, 15, 19, 20, 21}
	cases := []struct {
		name string
		off  int
	}{
		{"Калининград +2", 2},
		{"Москва +3", 3},
		{"Екатеринбург +5", 5},
		{"Красноярск +7", 7},
		{"Владивосток +10", 10},
		{"Камчатка +12", 12},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := guessUTCOffset(toUTC(local, tc.off))
			if got < tc.off-2 || got > tc.off+2 {
				t.Fatalf("offset=%d, ждали %d±2", got, tc.off)
			}
		})
	}
}

// Мало данных — не гадаем, а берём московские: одна ночная запись не должна
// утаскивать человека на другой конец страны.
func TestGuessUTCOffset_FallbackOnFewSamples(t *testing.T) {
	for _, hours := range [][]int{nil, {}, {3}, {1, 2, 3, 4}} {
		if got := guessUTCOffset(hours); got != mskOffsetHours {
			t.Fatalf("hours=%v: offset=%d, ждали %d", hours, got, mskOffsetHours)
		}
	}
}

// Равномерный разброс по суткам — направления нет, оценке верить нельзя.
func TestGuessUTCOffset_FallbackOnUniform(t *testing.T) {
	all := make([]int, 0, 24)
	for h := 0; h < 24; h++ {
		all = append(all, h)
	}
	if got := guessUTCOffset(all); got != mskOffsetHours {
		t.Fatalf("offset=%d на равномерных сутках, ждали %d", got, mskOffsetHours)
	}
}

// Результат всегда в правдоподобных границах — иначе итог уедет в ночь.
func TestGuessUTCOffset_AlwaysPlausible(t *testing.T) {
	for shift := 0; shift < 24; shift++ {
		hours := make([]int, 0, 8)
		for i := 0; i < 8; i++ {
			hours = append(hours, (shift+i)%24)
		}
		got := guessUTCOffset(hours)
		if got < minOffsetHours || got > maxOffsetHours {
			t.Fatalf("shift=%d: offset=%d вне [%d;%d]", shift, got, minOffsetHours, maxOffsetHours)
		}
	}
}
