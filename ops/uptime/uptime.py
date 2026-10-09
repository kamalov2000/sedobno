#!/usr/bin/env python3
"""Внешний мониторинг (решение владельца 09.10.2026, после падения dockerd на общем VPS).

Запускается ВНЕ сервера (GitHub Actions, .github/workflows/uptime.yml). Проверяет прод
«Съедобно» и Астру. Сообщает только о ПОДТВЕРЖДЁННОМ падении и о восстановлении:

  - падение подтверждено, если не прошли все ATTEMPTS попыток подряд с паузой PAUSE_S;
  - состояние — открытый issue с меткой `uptime` на каждую цель: открыт = цель лежит.
    Упала и issue нет → открыть issue и сообщить. Лежит и issue уже есть → молчать.
    Поднялась и issue есть → закрыть, сообщить, сколько лежала. Так нет ни спама на каждый
    неудачный запрос, ни повторов каждые N минут;
  - сообщение — issue в репозитории (уведомление GitHub) и, если заданы секреты
    UPTIME_TG_TOKEN и UPTIME_TG_CHAT, сообщение в Telegram.

Без зависимостей: только стандартная библиотека. DRY_RUN=1 — проверить цели и напечатать
решение, ничего не открывая и не отправляя.
"""
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from datetime import datetime, timezone

TARGETS = [
    # (ключ, человеческое имя, адрес)
    ("sedobno-api", "Съедобно — прод (API)", "https://sedobno-pay.ru/api/health"),
    ("sedobno-app", "Съедобно — прод (мини-приложение)", "https://sedobno-pay.ru/app/"),
    ("astra-dev", "Астра (dev)", "https://astra-dev.sedobno-pay.ru/"),
]
ATTEMPTS = int(os.environ.get("ATTEMPTS", "3"))
PAUSE_S = float(os.environ.get("PAUSE_S", "20"))
TIMEOUT_S = 10
LABEL = "uptime"
DRY = os.environ.get("DRY_RUN") == "1"


def probe(url):
    """(ok, описание) одной попытки. ok — ответ 2xx/3xx."""
    req = urllib.request.Request(url, headers={"User-Agent": "sedobno-uptime/1"})
    try:
        with urllib.request.urlopen(req, timeout=TIMEOUT_S) as r:
            return 200 <= r.status < 400, f"HTTP {r.status}"
    except urllib.error.HTTPError as e:
        return False, f"HTTP {e.code}"
    except Exception as e:  # таймаут, DNS, отказ соединения, TLS
        return False, type(e).__name__ + (f": {e.reason}" if hasattr(e, "reason") else "")


def check(url, attempts=ATTEMPTS, pause=PAUSE_S, probe_fn=probe, sleep=time.sleep):
    """Подтверждённое состояние: up, если прошла хоть одна попытка; down — если ни одна."""
    last = ""
    for i in range(attempts):
        ok, last = probe_fn(url)
        if ok:
            return True, last
        if i < attempts - 1:
            sleep(pause)
    return False, last


def decide(is_up, open_issue):
    """Что делать: 'open' (сообщить о падении), 'close' (о восстановлении) или None."""
    if not is_up and open_issue is None:
        return "open"
    if is_up and open_issue is not None:
        return "close"
    return None


# --- GitHub issues как хранилище состояния ---------------------------------------------

def gh(method, path, body=None):
    url = f"https://api.github.com/repos/{os.environ['GITHUB_REPOSITORY']}{path}"
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method, headers={
        "Authorization": f"Bearer {os.environ['GITHUB_TOKEN']}",
        "Accept": "application/vnd.github+json",
        "User-Agent": "sedobno-uptime/1",
    })
    with urllib.request.urlopen(req, timeout=20) as r:
        raw = r.read()
        return json.loads(raw) if raw else None


def ensure_label():
    """Метка состояния; уже есть — GitHub ответит 422, это нормально."""
    try:
        gh("POST", "/labels", {"name": LABEL, "color": "d73a4a", "description": "внешний мониторинг: цель недоступна"})
    except urllib.error.HTTPError as e:
        if e.code != 422:
            raise


def open_issues():
    """Открытые issue монитора по ключу цели (ключ — в заголовке в квадратных скобках)."""
    out = {}
    for it in gh("GET", f"/issues?state=open&labels={LABEL}&per_page=100") or []:
        title = it.get("title", "")
        if title.startswith("[") and "]" in title:
            out[title[1:title.index("]")]] = it
    return out


def telegram(text):
    token, chat = os.environ.get("UPTIME_TG_TOKEN"), os.environ.get("UPTIME_TG_CHAT")
    if not token or not chat:
        print("   (Telegram не настроен — сообщение только в issue)")
        return
    data = urllib.parse.urlencode({"chat_id": chat, "text": text}).encode()
    try:
        urllib.request.urlopen(f"https://api.telegram.org/bot{token}/sendMessage", data=data, timeout=15).read()
    except Exception as e:  # оповещение не должно ронять проверку остальных целей
        print(f"   Telegram: {type(e).__name__}")


def minutes_since(iso):
    t = datetime.fromisoformat(iso.replace("Z", "+00:00"))
    return int((datetime.now(timezone.utc) - t).total_seconds() // 60)


def main():
    now = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
    if not DRY:
        ensure_label()
    issues = {} if DRY else open_issues()
    failed = False
    for key, name, url in TARGETS:
        up, detail = check(url)
        issue = issues.get(key)
        action = decide(up, issue)
        print(f"{name}: {'работает' if up else 'НЕДОСТУПЕН'} ({detail}) → {action or 'ничего'}")
        if DRY or action is None:
            continue
        try:
            if action == "open":
                gh("POST", "/issues", {
                    "title": f"[{key}] {name} недоступен",
                    "labels": [LABEL],
                    "body": f"Подтверждено {now}: {ATTEMPTS} попытки подряд с паузой {int(PAUSE_S)} с не прошли.\n"
                            f"Адрес: {url}\nПоследний ответ: {detail}\n\n"
                            f"Восстановление: docs/incidents/2026-10-09-dockerd-crash.md",
                })
                telegram(f"🔴 {name} недоступен (подтверждено {now}).\n{url}\nПоследний ответ: {detail}")
            else:
                mins = minutes_since(issue["created_at"])
                gh("POST", f"/issues/{issue['number']}/comments", {"body": f"Восстановлено {now} ({detail})."})
                gh("PATCH", f"/issues/{issue['number']}", {"state": "closed"})
                telegram(f"🟢 {name} снова работает ({now}). Был недоступен около {mins} мин.")
        except Exception as e:
            failed = True
            print(f"   не удалось записать состояние: {type(e).__name__}: {e}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
