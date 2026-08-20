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

pid_is_localsend() {
    p="$1"
    [ -n "$p" ] || return 1
    kill -0 "$p" 2>/dev/null || return 1
    if [ -r "/proc/$p/cmdline" ]; then
        tr '\000' ' ' <"/proc/$p/cmdline" 2>/dev/null | grep -q 'localsend-kindle' || return 1
    fi
    return 0
}

running() {
    [ -f "$PIDFILE" ] || return 1
    pid="$(cat "$PIDFILE" 2>/dev/null)"
    pid_is_localsend "$pid"
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
    printf '%s KUAL start requested: wrapper v0.1.9, frozen core v0.1.7, HTTP v2.2, port 53317, receive=/mnt/us/documents\n' "$(date '+%Y-%m-%d %H:%M:%S' 2>/dev/null)" >>"$LOG"
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
    quiet="$1"
    if ! running; then
        rm -f "$PIDFILE" "$ROOT/state/daemon.lock"
        if "$BIN" firewall-cleanup --root "$ROOT" >>"$LOG" 2>&1; then
            [ "$quiet" = "quiet" ] || msg "LocalSend 当前未运行\n临时防火墙规则已清理"
            return 0
        fi
        [ "$quiet" = "quiet" ] || msg "LocalSend 当前未运行\n但防火墙清理失败\n请查看 logs/localsend.log"
        return 1
    fi

    pid="$(cat "$PIDFILE" 2>/dev/null)"
    kill "$pid" 2>/dev/null || true
    i=0
    while pid_is_localsend "$pid" && [ "$i" -lt 5 ]; do
        sleep 1
        i=$((i + 1))
    done
    if pid_is_localsend "$pid"; then
        kill -9 "$pid" 2>/dev/null || true
        i=0
        while pid_is_localsend "$pid" && [ "$i" -lt 2 ]; do
            sleep 1
            i=$((i + 1))
        done
    fi

    if pid_is_localsend "$pid"; then
        "$BIN" firewall-cleanup --root "$ROOT" >>"$LOG" 2>&1 || true
        [ "$quiet" = "quiet" ] || msg "LocalSend 停止不完整\n进程仍存在 PID=$pid\n请查看 logs/localsend.log"
        return 1
    fi

    rm -f "$PIDFILE" "$ROOT/state/daemon.lock"
    if ! "$BIN" firewall-cleanup --root "$ROOT" >>"$LOG" 2>&1; then
        [ "$quiet" = "quiet" ] || msg "LocalSend 进程已停止\n但防火墙清理失败\n请查看 logs/localsend.log"
        return 1
    fi
    [ "$quiet" = "quiet" ] || msg "LocalSend 已确认停止\n临时防火墙规则已恢复"
    return 0
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

install_refresh() {
    mkdir -p "$ROOT/state" "$ROOT/logs" /mnt/us/documents
    if "$BIN" menu --root "$ROOT" --write "$ROOT/menu.json" >>"$LOG" 2>&1; then
        count="$($BIN install-zip --root "$ROOT" --receive-dir /mnt/us/documents --list 2>/dev/null | wc -l | tr -d ' ')"
        msg "ZIP 列表已刷新\n发现 ${count:-0} 个可安装 ZIP\n请重新打开安装 ZIP 子菜单"
        return 0
    fi
    msg "刷新 ZIP 列表失败\n请查看 logs/localsend.log"
    return 1
}

install_select() {
    token="$1"
    [ -n "$token" ] || { msg "ZIP 选择参数无效"; return 1; }
    result="$($BIN install-zip --root "$ROOT" --receive-dir /mnt/us/documents --select "$token" 2>&1)"
    rc=$?
    if [ "$rc" -eq 0 ]; then
        msg "已选择待安装 ZIP\n$result\n再次进入安装 ZIP 并选择\n“确认安装已选 ZIP”"
    else
        msg "选择 ZIP 失败\n$result\n请刷新 ZIP 列表后重试"
    fi
    return "$rc"
}

install_cancel() {
    result="$($BIN install-zip --root "$ROOT" --cancel 2>&1)"
    rc=$?
    if [ "$rc" -eq 0 ]; then
        msg "已取消待安装 ZIP"
    else
        msg "取消失败\n$result"
    fi
    return "$rc"
}

install_confirm() {
    pending="$($BIN install-zip --root "$ROOT" --receive-dir /mnt/us/documents --pending 2>&1)"
    if [ "$?" -ne 0 ]; then
        msg "当前没有可确认的 ZIP\n请先刷新列表并选择一个 ZIP"
        return 1
    fi

    was_running=0
    if running; then
        was_running=1
        if ! stop_daemon quiet; then
            msg "安装已取消\nLocalSend 无法确认停止\n未修改 Kindle 文件"
            return 1
        fi
    fi

    rotate_log
    printf '%s KUAL ZIP install requested: %s\n' "$(date '+%Y-%m-%d %H:%M:%S' 2>/dev/null)" "$pending" >>"$LOG"
    result="$($BIN install-zip --root "$ROOT" --receive-dir /mnt/us/documents --dest-root /mnt/us --confirm 2>&1)"
    rc=$?

    restart_note=""
    if [ "$was_running" = "1" ]; then
        if start_daemon 0 >/dev/null 2>&1; then
            restart_note="\nLocalSend 已恢复运行"
        else
            restart_note="\nLocalSend 自动恢复失败，请手动启动"
        fi
    fi

    if [ "$rc" -eq 0 ]; then
        msg "ZIP 安装完成\n$result$restart_note\n如安装的是 KUAL 扩展，请重新打开 KUAL 查看新菜单"
    else
        msg "ZIP 安装失败\n$result$restart_note\n安装器已尝试自动回滚，详情见日志"
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
    install-refresh)
        install_refresh
        ;;
    install-select)
        install_select "$2"
        ;;
    install-confirm)
        install_confirm
        ;;
    install-cancel)
        install_cancel
        ;;
    help)
        msg "接收: HTTP v2.2\n保存: /mnt/us/documents\n核心: v0.1.7 双平台冻结\n加固: v0.1.9 外围稳定层\n支持安全安装接收的 ZIP 到 /mnt/us\n停止后会复核进程并恢复防火墙"
        ;;
    *)
        msg "LocalSend-KUAL v0.1.9\n未知命令: $1"
        exit 2
        ;;
esac
