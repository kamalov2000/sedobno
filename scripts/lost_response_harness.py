# Управляемый тест «потерянного ответа» — запуск:
#
#   1. initData синтетического аккаунта подписывается НА СЕРВЕРЕ токеном тестового бота
#      (токен не покидает VPS), как в scripts/smoke_prod.sh, только с deploy/.env.beta;
#      telegram_id — явно тестовый, 999xxxxxx. Строку сохранить в файл.
#   2. PYTHONIOENCODING=utf-8 INIT_DATA="$(cat init.txt)" python scripts/lost_response_harness.py
#   3. Открыть http://127.0.0.1:8898/app/ в браузере; перед нажатием сохранения —
#      curl http://127.0.0.1:8898/__drop?n=1 (или n=2 для двух обрывов подряд).
#   4. Журнал запросов — curl http://127.0.0.1:8898/__log; записи — в базе беты.
#   5. После проверки удалить синтетический аккаунт (events, затем users — каскад заберёт
#      meals и meal_templates).
#
# Прогон 08.10.2026 и его результаты — backlog.md, п.16, «Потерянный ответ».
# Управляемый тест «потерянного ответа» (задание владельца 08.10.2026).
#
# Стенд отдаёт ВЫКАЧЕННЫЙ на бету бандл мини-приложения с оболочкой Telegram (подписанный
# initData синтетического аккаунта) и проксирует /api на БЕТУ. По команде /__drop?n=N
# следующие N сохранений (POST /api/meals, POST /api/meal-templates/{id}/add) уходят на
# сервер ЦЕЛИКОМ — сервер их обрабатывает и записывает, — а ответ клиенту не передаётся:
# соединение закрывается. Это ровно «сервер сохранил → подтверждение не дошло», а не
# «нет сети до отправки».
#
# /__log — журнал: что ушло на сервер, что сервер ответил и было ли ответ оборван.
import http.server, json, os, ssl, threading, urllib.error, urllib.request

NOVERIFY = ssl._create_unverified_context()
TARGET = "https://beta.sedobno-pay.ru"
INIT = os.environ["INIT_DATA"]

STUB = (
    "<script>(function(){"
    "var noop=function(){};"
    "var anyObj=function(){return new Proxy({},{get:function(t,p){"
    "  if(p==='isVisible')return false; if(p==='text')return ''; return noop;}});};"
    "var base={initData:" + json.dumps(INIT) + ","
    "colorScheme:'light',themeParams:{},version:'7.10',platform:'web',"
    "isExpanded:true,viewportHeight:900,viewportStableHeight:900,"
    "HapticFeedback:anyObj(),MainButton:anyObj(),BackButton:anyObj(),"
    "SettingsButton:anyObj(),CloudStorage:anyObj()};"
    "window.Telegram={WebApp:new Proxy(base,{get:function(t,p){"
    "  return (p in t)?t[p]:noop;}})};"
    "})();</script>"
)

lock = threading.Lock()
state = {"drop": 0}
journal = []


def is_save(method, path):
    p = path.split("?")[0]
    return method == "POST" and (p == "/api/meals" or (p.startswith("/api/meal-templates/") and p.endswith("/add")))


def fetch(path, method="GET", body=None, headers=None):
    req = urllib.request.Request(TARGET + path, data=body, method=method)
    for k, v in (headers or {}).items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=120, context=NOVERIFY) as r:
            return r.status, r.read(), r.headers.get("Content-Type", "application/octet-stream")
    except urllib.error.HTTPError as e:
        return e.code, e.read(), e.headers.get("Content-Type", "application/json")


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def reply(self, code, data, ctype):
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(data)

    def passthrough(self, method):
        n = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(n) if n else None
        headers = {}
        for h in ("Authorization", "Content-Type", "X-Op-Id", "X-Route", "Accept"):
            v = self.headers.get(h)
            if v:
                headers[h] = v
        code, data, ctype = fetch(self.path, method, body, headers)
        if is_save(method, self.path):
            with lock:
                drop = state["drop"] > 0
                if drop:
                    state["drop"] -= 1
            try:
                sent = json.loads(body or b"{}")
            except ValueError:
                sent = None
            try:
                got = json.loads(data or b"{}")
            except ValueError:
                got = None
            journal.append({"path": self.path, "sent": sent, "server_status": code,
                            "server_id": (got or {}).get("id"), "server_kcal": ((got or {}).get("total") or {}).get("kcal"),
                            "response_dropped": drop})
            if drop:
                # Сервер уже записал. Клиенту — заголовки ответа и обрыв посреди тела: так
                # браузер НЕ переотправляет запрос сам (на обрыв до первого байта он
                # повторяет POST молча — это видно в журнале как второй вызов), и
                # приложение получает настоящий сбой чтения ответа.
                self.send_response(code)
                self.send_header("Content-Type", ctype)
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.flush()
                self.close_connection = True
                try:
                    self.connection.shutdown(2)
                except OSError:
                    pass
                return
        self.reply(code, data, ctype)

    def do_GET(self):
        if self.path.startswith("/__drop"):
            n = int(self.path.split("n=")[1]) if "n=" in self.path else 1
            with lock:
                state["drop"] = n
            self.reply(200, json.dumps({"drop": n}).encode(), "application/json")
            return
        if self.path.startswith("/__log"):
            self.reply(200, json.dumps(journal, ensure_ascii=False, indent=1).encode("utf-8"), "application/json; charset=utf-8")
            return
        if self.path in ("/", "/app", "/app/"):
            code, data, _ = fetch("/app/")
            html = data.decode("utf-8")
            sdk = '<script src="https://telegram.org/js/telegram-web-app.js"></script>'
            html = html.replace(sdk, STUB, 1) if sdk in html else html.replace("<head>", "<head>" + STUB, 1)
            self.reply(code, html.encode("utf-8"), "text/html; charset=utf-8")
            return
        self.passthrough("GET")

    def do_POST(self):
        self.passthrough("POST")

    def do_PATCH(self):
        self.passthrough("PATCH")

    def do_DELETE(self):
        self.passthrough("DELETE")


if __name__ == "__main__":
    print("стенд потерянного ответа на http://127.0.0.1:8898/ → " + TARGET, flush=True)
    http.server.ThreadingHTTPServer(("127.0.0.1", 8898), Handler).serve_forever()
