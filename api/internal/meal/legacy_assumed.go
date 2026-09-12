package meal

import (
	"math"

	"github.com/sostav/api/internal/llm"
)

// НАСЛЕДНЫЕ ПРЕДПОЛОЖЕННЫЕ ДОБАВКИ.
//
// До 11.09.2026 предположенная добавка (ложка масла, сахар) входила в итог приёма молча.
// Релиз маршрутизации это правило перевернул: предположенное показывается чипсом «≈», но в
// итог входит только после подтверждения тапом. У приёмов, сохранённых РАНЬШЕ, добавка уже
// сидит в сохранённом числе — и первый же PATCH пересчитал бы итог по новому правилу,
// уронив человеку калории без единого слова. На 12.09.2026 таких приёмов 38 у 9 человек,
// суммарно 2226 ккал (backlog п.11).
//
// Поэтому наследный приём сервер узнаёт по САМОЙ СОХРАНЁННОЙ ЗАПИСИ, до применения правки:
// в блюдах есть assumed-куски И сохранённый итог совпадает с суммой, куда они включены.
// Приём, сохранённый после релиза, под это условие не подходит: его итог совпадает с
// суммой БЕЗ предположенного, и наследным он не считается.
//
// Базу не мигрируем: набор закрыт (новых таких приёмов не появляется) и рассасывается сам
// по мере того, как люди отвечают на вопрос «оставить или удалить».

// Решения человека по наследным добавкам — поле assumed в PATCH /api/meals/{id}.
const (
	AssumedKeep   = "keep"   // добавки становятся подтверждёнными
	AssumedRemove = "remove" // добавки удаляются, итог уменьшается осознанно
)

// assumedTolerance — допуск сверки сохранённого итога с пересчитанным. Числа в meals
// прошли round1, поэтому расхождение в десятые — это арифметика, а не смысл. Ложка самой
// дешёвой добавки (сахар) стоит 20 ккал, так что допуск в 1 их не спутает.
const assumedTolerance = 1.0

// AssumedAddon — предположенная добавка, которая УЖЕ входит в сохранённый итог приёма.
// Уходит клиенту, чтобы он назвал человеку продукт и цену вопроса, а не спрашивал вслепую.
type AssumedAddon struct {
	Kind  string  `json:"kind"`
	Label string  `json:"label"`
	Count int     `json:"count"`
	Kcal  float64 `json:"kcal"`
}

// ValidAssumedDecision — решение понятно серверу. Пустая строка допустима: это «решения не
// было» (старый клиент или человек закрыл вопрос), и она НЕ равна «удалить».
func ValidAssumedDecision(v string) bool {
	return v == "" || v == AssumedKeep || v == AssumedRemove
}

// assumedMacros — вклад предположенных кусков: ровно то, чего сегодняшний sumMacros в итог
// не берёт.
func assumedMacros(dishes []llm.MealItem) llm.MealMacros {
	reg := pieceRegistry()
	var m llm.MealMacros
	for _, d := range dishes {
		for _, p := range d.Pieces {
			if !p.Assumed {
				continue
			}
			spec, ok := reg[p.Kind]
			if !ok {
				continue
			}
			n := float64(p.Count)
			m.Kcal += n * spec.Kcal
			m.ProteinG += n * spec.Protein
			m.FatG += n * spec.Fat
			m.CarbG += n * spec.Carb
		}
	}
	return m
}

func macrosClose(a, b llm.MealMacros) bool {
	return math.Abs(a.Kcal-b.Kcal) <= assumedTolerance &&
		math.Abs(a.ProteinG-b.ProteinG) <= assumedTolerance &&
		math.Abs(a.FatG-b.FatG) <= assumedTolerance &&
		math.Abs(a.CarbG-b.CarbG) <= assumedTolerance
}

func plusMacros(a, b llm.MealMacros) llm.MealMacros {
	return llm.MealMacros{
		Kcal:     round1(a.Kcal + b.Kcal),
		ProteinG: round1(a.ProteinG + b.ProteinG),
		FatG:     round1(a.FatG + b.FatG),
		CarbG:    round1(a.CarbG + b.CarbG),
	}
}

// AssumedInTotal возвращает предположенные добавки приёма, если сохранённый итог их
// ВКЛЮЧАЕТ (наследный приём). Пусто — обычный приём: предположенного нет, или оно уже вне
// итога, или сохранённые числа не сходятся ни с одной версией (ручная правка через API,
// старые схемы) — в сомнительном случае ничего не выдумываем.
func AssumedInTotal(dishes []llm.MealItem, stored llm.MealMacros) []AssumedAddon {
	extra := assumedMacros(dishes)
	if extra.Kcal <= 0 {
		return nil
	}
	without := sumMacros(dishes)
	with := plusMacros(without, extra)
	if macrosClose(stored, without) || !macrosClose(stored, with) {
		return nil
	}

	reg := pieceRegistry()
	out := make([]AssumedAddon, 0, 2)
	for _, d := range dishes {
		for _, p := range d.Pieces {
			if !p.Assumed {
				continue
			}
			spec, ok := reg[p.Kind]
			if !ok {
				continue
			}
			out = append(out, AssumedAddon{
				Kind: p.Kind, Label: PieceLabels[p.Kind], Count: p.Count,
				Kcal: round1(float64(p.Count) * spec.Kcal),
			})
		}
	}
	return out
}

// ApplyAssumedDecision приводит блюда правки к решению человека и считает итог.
//
//   - keep   — добавки становятся подтверждёнными: флаг снят, дальше они обычные куски;
//   - remove — добавки исчезают из блюд, итог уменьшается ровно на них и один раз;
//   - ""     — решения не было (старый клиент): куски остаются предположенными, но итог их
//     ПО-ПРЕЖНЕМУ включает. Сохранённое число не уменьшается само — ни при пустой правке,
//     ни при изменении калорий самого блюда.
//
// Вызывать ТОЛЬКО для наследного приёма (AssumedInTotal непуст): у обычного приёма решение
// ничего не значит, и «keep» там молча ПРИБАВИЛ бы калории.
func ApplyAssumedDecision(dishes []llm.MealItem, decision string) ([]llm.MealItem, llm.MealMacros) {
	switch decision {
	case AssumedKeep:
		out := cloneDishes(dishes)
		for di := range out {
			for pi := range out[di].Pieces {
				out[di].Pieces[pi].Assumed = false
			}
		}
		return out, sumMacros(out)

	case AssumedRemove:
		out := cloneDishes(dishes)
		for di := range out {
			kept := make([]llm.MealPiece, 0, len(out[di].Pieces))
			for _, p := range out[di].Pieces {
				if !p.Assumed {
					kept = append(kept, p)
				}
			}
			if len(kept) == 0 {
				kept = nil
			}
			out[di].Pieces = kept
		}
		return out, sumMacros(out)

	default:
		return dishes, plusMacros(sumMacros(dishes), assumedMacros(dishes))
	}
}

// cloneDishes копирует блюда вместе с их кусками: решение правит куски, а входной срез
// принадлежит вызывающему (и в тестах — литералу).
func cloneDishes(in []llm.MealItem) []llm.MealItem {
	out := make([]llm.MealItem, len(in))
	copy(out, in)
	for i := range out {
		if out[i].Pieces == nil {
			continue
		}
		ps := make([]llm.MealPiece, len(out[i].Pieces))
		copy(ps, out[i].Pieces)
		out[i].Pieces = ps
	}
	return out
}
