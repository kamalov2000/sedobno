package server

import (
	"testing"

	"github.com/sostav/api/internal/llm"
)

func img(b ...byte) llm.Image { return llm.Image{Data: b, MIME: "image/jpeg"} }

// Ключ кэша обязан быть функцией от кадров и только от них: одинаковые байты — один
// хэш, иначе повторная отправка того же файла в кэш не попадёт и весь смысл пропадёт.
func TestImagesSHA_ОдинаковыеКадрыОдинХэш(t *testing.T) {
	a := imagesSHA([]llm.Image{img(1, 2, 3), img(4, 5)})
	b := imagesSHA([]llm.Image{img(1, 2, 3), img(4, 5)})
	if a != b {
		t.Errorf("хэш нестабилен: %s != %s", a, b)
	}
}

func TestImagesSHA_РазныеКадрыРазныйХэш(t *testing.T) {
	if a, b := imagesSHA([]llm.Image{img(1, 2, 3)}), imagesSHA([]llm.Image{img(1, 2, 4)}); a == b {
		t.Errorf("разные кадры дали один хэш %s", a)
	}
}

// Порядок кадров значим: состав склеивается ОТ КАДРА К КАДРУ, и переставленные местами
// снимки — это другой вход, который может дать другой список ингредиентов.
func TestImagesSHA_ПорядокКадровЗначим(t *testing.T) {
	a := imagesSHA([]llm.Image{img(1, 2), img(3, 4)})
	b := imagesSHA([]llm.Image{img(3, 4), img(1, 2)})
	if a == b {
		t.Errorf("перестановка кадров не изменила хэш (%s)", a)
	}
}

// Границы кадров подмешиваются в хэш длиной: без этого наборы [AB][C] и [A][BC] дали бы
// один ключ, и двухкадровый скан получил бы ответ от трёхкадрового.
func TestImagesSHA_ГраницыКадровРазличимы(t *testing.T) {
	a := imagesSHA([]llm.Image{img(1, 2), img(3)})
	b := imagesSHA([]llm.Image{img(1), img(2, 3)})
	if a == b {
		t.Errorf("разная нарезка одних байт дала один хэш (%s)", a)
	}
}

func TestImagesSHA_ПустойНабор(t *testing.T) {
	// Пустой набор до кэша не доходит (обработчик отвергает скан без фото), но хэш
	// обязан быть определён, а не паниковать на срезке sha[:12] в логе.
	if got := imagesSHA(nil); len(got) != 64 {
		t.Errorf("длина хэша = %d, ждали 64 hex-символа", len(got))
	}
}
