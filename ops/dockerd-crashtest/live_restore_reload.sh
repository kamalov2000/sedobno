#!/bin/sh
# Опыт 09.10.2026: включение live-restore через SIGHUP (= systemctl reload docker) без
# остановки dockerd, затем аварийное завершение; опубликованный порт должен отвечать.
# Запуск — как restart_policy.sh (docker:29.6.0-dind с томами), без аргументов.
# Процедура для сервера: включить live-restore перезагрузкой настроек (SIGHUP = systemctl
# reload docker), без остановки dockerd; затем аварийное завершение; опубликованный порт
# должен отвечать и после него.
mkdir -p /etc/docker
echo '{}' > /etc/docker/daemon.json
containerd >/tmp/containerd.log 2>&1 &
for i in $(seq 1 30); do [ -S /run/containerd/containerd.sock ] && break; sleep 1; done
( while true; do
    dockerd --containerd=/run/containerd/containerd.sock >>/tmp/dockerd.log 2>&1
    echo "$(date -u +%T) dockerd exited code=$?" >> /tmp/sup.log
    sleep 2
  done ) &
for i in $(seq 1 60); do docker info >/dev/null 2>&1 && break; sleep 1; done
docker pull -q busybox:1.36 >/dev/null
docker run -d --name web --restart unless-stopped -p 8099:80 busybox:1.36 sh -c 'echo ok > /i.html; httpd -f -p 80 -h /' >/dev/null
sleep 3
probe(){ printf '%s: порт=%s, контейнер=%s\n' "$1" "$(wget -qO- -T 3 http://127.0.0.1:8099/i.html 2>/dev/null || echo НЕТ)" "$(docker inspect -f '{{.State.Status}}' web 2>/dev/null)"; }
probe "до"
PID=$(pidof dockerd)
echo "live-restore до: $(docker info --format '{{.LiveRestoreEnabled}}')"
echo '{"live-restore": true}' > /etc/docker/daemon.json
dockerd --validate --config-file /etc/docker/daemon.json && echo "конфиг: valid"
kill -HUP "$PID"; sleep 2
echo "live-restore после SIGHUP: $(docker info --format '{{.LiveRestoreEnabled}}'); pid тот же: $([ "$(pidof dockerd)" = "$PID" ] && echo да || echo НЕТ)"
probe "после reload"
kill -QUIT "$PID"; sleep 1
probe "dockerd упал"
for i in $(seq 1 60); do sleep 1; N=$(pidof dockerd); [ -n "$N" ] && [ "$N" != "$PID" ] && docker info >/dev/null 2>&1 && break; done
sleep 4
cat /tmp/sup.log
probe "dockerd перезапущен"
docker inspect -f 'restarts={{.RestartCount}} started={{.State.StartedAt}} finished={{.State.FinishedAt}}' web
