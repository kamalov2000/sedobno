package server

// Единый слой обязательных событий воронки (ТЗ, раздел 10). Отдельным файлом, потому что
// раньше события заводились по месту и назывались как придётся: scan_ok, meal_ok,
// pay_renewal_ok. Для продукта этого хватало, для воронки — нет: по ним нельзя связать
// пост, кнопку сайта, первый разбор, сохранение и оплату в один путь.
//
// Прежние имена НЕ трогаем — на них завязаны старые отчёты и скрипты (daily_funnel.sh).
// Новые идут рядом, с параметрами из ТЗ. Дублирование дешевле, чем переписывание истории.
//
// ЧТО СЮДА НЕ КЛАДЁМ (ТЗ прямо запрещает): состав, фотографии, telegram-имя и любые
// пользовательские данные. Только идентификатор пользователя, техника и деньги.

import "context"

// Имена обязательных событий. Строками в одном месте, чтобы опечатка в имени не
// расщепляла воронку надвое молча.
const (
	evBotStart             = "bot_start"
	evMealAnalysisSuccess  = "meal_analysis_success"
	evLabelAnalysisSuccess = "label_analysis_success"
	evResultEdited         = "result_edited"
	evDiaryEntrySaved      = "diary_entry_saved"
	evPaymentStarted       = "payment_started"
	evPaymentSuccess       = "payment_success"
	evSubscriptionRenewed  = "subscription_renewed"
	evLandingView          = "landing_view"
	evLandingCTAClick      = "landing_cta_click"
)

// track — обёртка над InsertEvent: аналитика best-effort и НИКОГДА не влияет на ответ
// пользователю. Ошибку глотаем осознанно (как и прежние вызовы), но в одном месте.
func (s *Server) track(ctx context.Context, userID int64, name string, props map[string]any) {
	_ = s.store.InsertEvent(ctx, &userID, name, props)
}

// trackAnon — событие без пользователя (лендинг: человек ещё не в боте).
func (s *Server) trackAnon(ctx context.Context, name string, props map[string]any) {
	_ = s.store.InsertEvent(ctx, nil, name, props)
}
