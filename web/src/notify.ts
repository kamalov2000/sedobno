// Уведомления (решение владельца 08.10.2026): одно добровольное напоминание в день во
// время, которое выбрал человек; вечерний итог; часовой пояс — с устройства, с
// возможностью поправить. Правила отправки живут на сервере (notify_policy.go).

/** Пояс устройства (IANA). Пусто — браузер не знает. */
export function deviceTZ(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone ?? "";
  } catch {
    return "";
  }
}

/** Пояса России для ручного выбора — от Калининграда до Камчатки. */
export const RU_ZONES: { tz: string; label: string }[] = [
  { tz: "Europe/Kaliningrad", label: "Калининград (МСК−1)" },
  { tz: "Europe/Moscow", label: "Москва (МСК)" },
  { tz: "Europe/Samara", label: "Самара (МСК+1)" },
  { tz: "Asia/Yekaterinburg", label: "Екатеринбург (МСК+2)" },
  { tz: "Asia/Omsk", label: "Омск (МСК+3)" },
  { tz: "Asia/Novosibirsk", label: "Новосибирск (МСК+4)" },
  { tz: "Asia/Krasnoyarsk", label: "Красноярск (МСК+4)" },
  { tz: "Asia/Irkutsk", label: "Иркутск (МСК+5)" },
  { tz: "Asia/Yakutsk", label: "Якутск (МСК+6)" },
  { tz: "Asia/Vladivostok", label: "Владивосток (МСК+7)" },
  { tz: "Asia/Magadan", label: "Магадан (МСК+8)" },
  { tz: "Asia/Kamchatka", label: "Камчатка (МСК+9)" },
];

export function tzLabel(tz: string): string {
  return RU_ZONES.find((z) => z.tz === tz)?.label ?? (tz || "Москва (МСК)");
}

/** Время по умолчанию в предложении — обед: самый частый пропуск записи днём. */
export const DEFAULT_REMINDER_TIME = "13:00";

/** «2026-10-15» → «15.10». */
export function fmtDayShort(day: string): string {
  const [, m, d] = day.split("-");
  return d && m ? `${d}.${m}` : day;
}
