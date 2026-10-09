#!/bin/sh
# Опыт 09.10.2026 (docs/incidents/2026-10-09-dockerd-crash.md): аварийное или штатное завершение
# dockerd при отдельном containerd. Запуск ВНЕ боевого сервера, внутри docker:29.6.0-dind:
#   docker run -d --privileged --name crashtest -v crashtest-ctd:/var/lib/containerd -v crashtest-dkr:/var/lib/docker --entrypoint sh docker:29.6.0-dind -c "sleep 3600"
#   docker cp ops/dockerd-crashtest/restart_policy.sh crashtest:/t.sh && docker exec crashtest sh /t.sh off QUIT   # off|on — live-restore; QUIT|TERM
# Тома для /var/lib/* обязательны: overlay поверх overlay внутри dind не монтируется.
# Воспроизведение 09.10.2026: аварийное завершение dockerd при отдельном containerd.
# $1 = "off" | "on" — live-restore.
LR="$1"
mkdir -p /etc/docker
if [ "$LR" = on ]; then echo '{"live-restore": true}' > /etc/docker/daemon.json; else echo '{}' > /etc/docker/daemon.json; fi

containerd >/tmp/containerd.log 2>&1 &
for i in $(seq 1 30); do [ -S /run/containerd/containerd.sock ] && break; sleep 1; done

# «systemd»: Restart=always, RestartSec=2 (как docker.service на сервере)
( while true; do
    dockerd --containerd=/run/containerd/containerd.sock >>/tmp/dockerd.log 2>&1
    echo "$(date -u +%T) dockerd exited code=$?" >> /tmp/sup.log
    sleep 2
  done ) &

for i in $(seq 1 60); do docker info >/dev/null 2>&1 && break; sleep 1; done
docker info --format 'live-restore={{.LiveRestoreEnabled}} version={{.ServerVersion}}'
docker pull -q alpine:3.20 >/dev/null
docker network create appnet >/dev/null
docker run -d --name plain   --restart unless-stopped --network appnet alpine:3.20 sleep 1d >/dev/null
docker run -d --name always  --restart always         --network appnet alpine:3.20 sleep 1d >/dev/null
docker run -d --name health  --restart unless-stopped --network appnet \
  --health-cmd 'true' --health-interval 2s alpine:3.20 sleep 1d >/dev/null
docker run -d --name portpub --restart unless-stopped -p 8099:80 alpine:3.20 sleep 1d >/dev/null
sleep 5
echo "--- до падения"; docker ps -a --format '{{.Names}} {{.Status}}'

SIG="${2:-QUIT}"; PID=$(pidof dockerd); echo "--- SIG$SIG dockerd pid=$PID"
kill -$SIG "$PID"
for i in $(seq 1 60); do
  sleep 1
  NEW=$(pidof dockerd); [ -n "$NEW" ] && [ "$NEW" != "$PID" ] && docker info >/dev/null 2>&1 && break
done
sleep 8
cat /tmp/sup.log
echo "--- после перезапуска dockerd"; docker ps -a --format '{{.Names}} {{.Status}}'
for c in plain always health portpub; do
  docker inspect -f '{{.Name}} restart={{.HostConfig.RestartPolicy.Name}} running={{.State.Running}} exit={{.State.ExitCode}} restarts={{.RestartCount}} started={{.State.StartedAt}} finished={{.State.FinishedAt}}' $c
done
echo "--- журнал нового dockerd (восстановление)"
grep -E "Restoring|restart-manager|restart|Loading containers|live" /tmp/dockerd.log | tail -25 | cut -c1-200
