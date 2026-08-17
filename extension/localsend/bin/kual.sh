#!/bin/sh

ROOT="$(cd "$(dirname "$0")/.." 2>/dev/null && pwd)"
BIN="$ROOT/bin/localsend-kindle"
PIDFILE="$ROOT/state/daemon.pid"
LOG="$ROOT/logs/localsend.log"
OUTBOX="/mnt/us/LocalSend/Outbox"

msg() {
    text="$1"
    if [ -n "$KUAL" ]; then
        "$KUAL" 1 "$text"
    elif command -v eips >/dev/null 2>&1; then
        eips 2 38 "$text"
    else
        echo "$text"
    fi
}

running() {
    [ -f "$PIDFILE" ] || return 1
    pid="$(cat "$PIDFILE" 2>/dev/null)"
    [ -n "$pid" ] || return 1
    kill -0 "$pid" 2>/dev/null || return 1
    if [ -r "/proc/$pid/cmdline" ]; then
        tr '\000' ' ' <"/proc/$pid/cmdline" 2>/dev/null | grep -q 'localsend-kindle' || return 1
    fi
    return 0
}

rotate_log() {
    [ -f "$LOG" ] || return 0
    size="$(wc -c <"$LOG" 2>/dev/null | tr -d ' ')"
    case "$size" in
        ''|*[!0-9]*) return 0 ;;
    esac
    if [ "$size" -ge 1048576 ]; then
        rm -f "$LOG.1"
        mv "$LOG" "$LOG.1" 2>/dev/null || true
    fi
}

start_daemon() {
    mins="$1"
    if running; then
        msg "LocalSend 已在运行\n无需重复启动"
        return 0
    fi
    mkdir -p "$ROOT/state" "$ROOT/logs" /mnt/us/documents "$OUTBOX"
    rotate_log
    printf '%s KUAL start requested: wrapper v0.1.8, frozen core v0.1.7, HTTP v2.2, port 53317, receive=/mnt/us/documents\n' "$(date '+%Y-%m-%d %H:%M:%S' 2>/dev/null)" >>"$LOG"
    "$BIN" serve --root "$ROOT" --duration "$mins" --compat-http --receive-dir /mnt/us/documents >>"$LOG" 2>&1 &
    i=0
    ready=0
    diag=""
    while [ "$i" -lt 10 ]; do
        if running; then
            diag="$($BIN selftest --root "$ROOT" 2>&1)"
            if [ "$?" -eq 0 ]; then
                ready=1
                break
            fi
        fi
        sleep 1
        i=$((i + 1))
    done
    printf '%s\n' "$diag" >>"$LOG"
    if [ "$ready" -eq 1 ]; then
        if [ "$mins" = "0" ]; then
            msg "LocalSend 已启动\n兼容接收模式 HTTP\n端口 53317"
        else
            msg "LocalSend 已启动\nHTTP 接收 ${mins} 分钟\n端口 53317"
        fi
    else
        if running; then
            pid="$(cat "$PIDFILE" 2>/dev/null)"
            [ -n "$pid" ] && kill "$pid" 2>/dev/null
        fi
        "$BIN" firewall-cleanup --root "$ROOT" >>"$LOG" 2>&1 || true
        msg "LocalSend 启动自检失败\n已安全停止并恢复防火墙\n请查看 logs/localsend.log"
        return 1
    fi
}

stop_daemon() {
    if ! running; then
        rm -f "$PIDFILE"
        "$BIN" firewall-cleanup --root "$ROOT" >>"$LOG" 2>&1 || true
        msg "LocalSend 当前未运行\n已清理临时防火墙规则"
        return 0
    fi
    pid="$(cat "$PIDFILE")"
    kill "$pid" 2>/dev/null
    i=0
    while kill -0 "$pid" 2>/dev/null && [ "$i" -lt 5 ]; do
        sleep 1
        i=$((i + 1))
    done
    if kill -0 "$pid" 2>/dev/null; then
        kill -9 "$pid" 2>/dev/null
    fi
    rm -f "$PIDFILE"
    "$BIN" firewall-cleanup --root "$ROOT" >>"$LOG" 2>&1 || true
    msg "LocalSend 已停止\n临时防火墙规则已恢复"
}

network_diagnostics() {
    result="$($BIN selftest --root "$ROOT" 2>&1)"
    printf '%s\n' "$result" >>"$LOG"
    msg "$result"
}

refresh_devices() {
    if ! running; then
        start_daemon 10 >/dev/null 2>&1
    fi
    if running; then
        pid="$(cat "$PIDFILE")"
        kill -USR1 "$pid" 2>/dev/null
        sleep 3
    fi
    "$BIN" menu --root "$ROOT" --write "$ROOT/menu.json" >>"$LOG" 2>&1
    count="$($BIN peers --root "$ROOT" 2>/dev/null | wc -l | tr -d ' ')"
    msg "设备列表已刷新\n发现 ${count:-0} 台设备\n菜单将自动刷新"
}

send_outbox() {
    peer="$1"
    if [ ! -d "$OUTBOX" ]; then
        mkdir -p "$OUTBOX"
    fi
    count=0
    for f in "$OUTBOX"/*; do
        [ -f "$f" ] && count=$((count + 1))
    done
    if [ "$count" = "0" ]; then
        msg "Outbox 为空\n请把文件放到\n/mnt/us/LocalSend/Outbox"
        return 1
    fi
    result="$($BIN send --root "$ROOT" --peer "$peer" --outbox "$OUTBOX" 2>&1)"
    rc=$?
    if [ "$rc" -eq 0 ]; then
        msg "发送完成\n$result\nOutbox 文件未自动删除"
    else
        msg "发送失败\n$result"
    fi
    return "$rc"
}

case "$1" in
    start)
        start_daemon "${2:-0}"
        ;;
    stop)
        stop_daemon
        ;;
    status)
        msg "$($BIN status --root "$ROOT" --short 2>/dev/null)"
        ;;
    refresh)
        refresh_devices
        ;;
    send)
        send_outbox "$2"
        ;;
    diagnose)
        network_diagnostics
        ;;
    help)
        msg "接收: HTTP v2.2\n保存: /mnt/us/documents\n核心: v0.1.7 双平台冻结\n加固: v0.1.8 外围稳定层\n停止后自动恢复防火墙"
        ;;
    *)
        msg "LocalSend-KUAL v0.1.8\n未知命令: $1"
        exit 2
        ;;
esac
