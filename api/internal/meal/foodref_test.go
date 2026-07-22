package meal

import (
	"strings"
	"testing"
)

// Справочник грузится, непустой, все позиции валидны (имя есть, ккал > 0, БЖУ не
// отрицательны). Ловит опечатку в JSON (битая позиция) и пустой файл.
func TestFoodReference_Валиден(t *testing.T) {
	ref := FoodReference()
	if len(ref) < 100 {
		t.Fatalf("в справочнике %d позиций, ждали ≥100", len(ref))
	}
	seen := map[string]bool{}
	for _, it := range ref {
		if strings.TrimSpace(it.Name) == "" {
			t.Errorf("пустое имя позиции: %+v", it)
		}
		if it.KcalPer100 <= 0 {
			t.Errorf("%q: ккал/100=%v, ждали >0", it.Name, it.KcalPer100)
		}
		if it.ProteinG < 0 || it.FatG < 0 || it.CarbG < 0 {
			t.Errorf("%q: отрицательный БЖУ %v/%v/%v", it.Name, it.ProteinG, it.FatG, it.CarbG)
		}
		if seen[it.Name] {
			t.Errorf("дубль позиции: %q", it.Name)
		}
		seen[it.Name] = true
	}
}

// КЛЮЧЕВОЕ: яйцо и белок в справочнике = константам eggs.go (пересчёт штука→100 г).
// Единый источник: справочное яйцо и счётчик штук не должны разъезжаться.
func TestFoodReference_ЯйцаСогласованыСEggs(t *testing.T) {
	ref := FoodReference()
	byName := map[string]FoodRefItem{}
	for _, it := range ref {
		byName[it.Name] = it
	}
	e := EggConsts()
	// целое яйцо: kcal_per_100 == WholeKcal/WholeGrams*100.
	whole, ok := byName["Яйцо куриное целое"]
	if !ok {
		t.Fatal("нет позиции «Яйцо куриное целое»")
	}
	wantWhole := round1(e.WholeKcal / e.WholeGrams * 100)
	if whole.KcalPer100 != wantWhole {
		t.Errorf("целое яйцо ккал/100=%v, ждали %v (из eggs.go)", whole.KcalPer100, wantWhole)
	}
	if whole.ProteinG != round1(e.WholeProtein/e.WholeGrams*100) {
		t.Errorf("целое яйцо белок/100=%v рассинхрон с eggs.go", whole.ProteinG)
	}
	// белок.
	white, ok := byName["Белок яичный"]
	if !ok {
		t.Fatal("нет позиции «Белок яичный»")
	}
	if white.KcalPer100 != round1(e.WhiteKcal/e.WhiteGrams*100) {
		t.Errorf("белок ккал/100=%v рассинхрон с eggs.go", white.KcalPer100)
	}
}
