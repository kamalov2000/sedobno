/** Русские числительные: «1 день», «2 дня», «5 дней».
 *
 *  Отдельным модулем, потому что нужны уже трём экранам (пейволл, главный, дневник), а
 *  склейка через «день(ей)» читается как машинный текст — особенно на экране, где
 *  человеку говорят про деньги и сроки.
 *
 *  Существующие частные хелперы (StreakCard, meal.ts, mealStoryCard) намеренно НЕ трогаю:
 *  они работают, а переписывать их за три дня до платного размещения — риск без выгоды.
 *  Новый код берёт этот. */
export function pluralRu(n: number, one: string, few: string, many: string): string {
  const m = Math.abs(n) % 100;
  if (m >= 11 && m <= 14) return many;
  switch (m % 10) {
    case 1:
      return one;
    case 2:
    case 3:
    case 4:
      return few;
    default:
      return many;
  }
}

/** «6 дней» — число вместе со словом. */
export function pluralDaysRu(n: number): string {
  return `${n} ${pluralRu(n, "день", "дня", "дней")}`;
}
