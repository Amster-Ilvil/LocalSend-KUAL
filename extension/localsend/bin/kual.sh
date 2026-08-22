#!/bin/sh

ROOT="$(cd "$(dirname "$0")/.." 2>/dev/null && pwd)"
BIN="$ROOT/bin/localsend-kindle"
PIDFILE="$ROOT/state/daemon.pid"
STARTPIDFILE="$ROOT/state/launch.pid"
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

pid_is_daemon() {
    p="$1"
    [ -n "$p" ] || return 1
    kill -0 "$p" 2>/dev/null || return 1
    if [ -r "/proc/$p/cmdline" ]; then
        tr '\000' ' ' <"/proc/$p/cmdline" 2>/dev/null | grep -q 'localsend-kindle' || return 1
        tr '\000' '\n' <"/proc/$p/cmdline" 2>/dev/null | grep -qx 'serve' || return 1
    fi
    return 0
}

daemon_pid() {
    for f in "$PIDFILE" "$ROOT/state/daemon.lock" "$STARTPIDFILE"; do
        [ -f "$f" ] || continue
        p="$(cat "$f" 2>/dev/null | tr -cd '0-9')"
        if pid_is_daemon "$p"; then
            echo "$p"
            return 0
        fi
    done
    return 1
}

running() {
    daemon_pid >/dev/null 2>&1
}

terminate_pid_strict() {
    p="$1"
    pid_is_daemon "$p" || return 0
    kill "$p" 2>/dev/null || true
    i=0
    while pid_is_daemon "$p" && [ "$i" -lt 5 ]; do
        sleep 1
        i=$((i + 1))
    done
    if pid_is_daemon "$p"; then
        kill -9 "$p" 2>/dev/null || true
        i=0
        while pid_is_daemon "$p" && [ "$i" -lt 2 ]; do
            sleep 1
            i=$((i + 1))
        done
    fi
    pid_is_daemon "$p" && return 1
    return 0
}

ensure_log() {
    mkdir -p "$ROOT/logs" || return 1
    if [ ! -f "$LOG" ]; then
        : >"$LOG" || return 1
    fi
    chmod 600 "$LOG" 2>/dev/null || true
    [ ! -f "$LOG.1" ] || chmod 600 "$LOG.1" 2>/dev/null || true
    return 0
}

rotate_log() {
    ensure_log || return 1
    size="$(wc -c <"$LOG" 2>/dev/null | tr -d ' ')"
    case "$size" in
        ''|*[!0-9]*) return 0 ;;
    esac
    if [ "$size" -ge 1048576 ]; then
        # A malformed leftover at logs/localsend.log.1 must never let the
        # current log grow without bound. If the backup cannot be removed or
        # the rename fails, truncate the current log as the low-risk fallback.
        rm -rf "$LOG.1" 2>/dev/null || true
        if mv "$LOG" "$LOG.1" 2>/dev/null; then
            chmod 600 "$LOG.1" 2>/dev/null || true
            : >"$LOG" || return 1
            chmod 600 "$LOG" 2>/dev/null || true
        else
            : >"$LOG" || return 1
            chmod 600 "$LOG" 2>/dev/null || true
        fi
    fi
}

firewall_cleanup_strict() {
    # Stop/firewall restoration must not depend on the log file being writable.
    # Shell redirection is evaluated before the command; if logs/ is missing or
    # damaged, redirecting directly to $LOG could otherwise prevent cleanup from
    # running at all.
    if ensure_log >/dev/null 2>&1; then
        "$BIN" firewall-cleanup --root "$ROOT" >>"$LOG" 2>&1
    else
        "$BIN" firewall-cleanup --root "$ROOT" >/dev/null 2>&1
    fi
}

start_daemon() {
    mins="$1"
    case "$mins" in
        ''|*[!0-9]*) msg "接收时长参数无效"; return 1 ;;
    esac
    if running; then
        msg "LocalSend 已在运行\n无需重复启动"
        return 0
    fi
    if ! mkdir -p "$ROOT/state" "$ROOT/logs" /mnt/us/documents "$OUTBOX"; then
        msg "LocalSend 启动失败\n无法创建运行目录"
        return 1
    fi
    rm -f "$STARTPIDFILE"
    if ! rotate_log; then
        msg "LocalSend 启动失败\n无法准备运行日志"
        return 1
    fi
    printf '%s KUAL start requested: v0.1.18 audited result dialog, audited core v0.1.11, HTTP v2.2, receive=/mnt/us/documents\n' "$(date '+%Y-%m-%d %H:%M:%S' 2>/dev/null)" >>"$LOG"
    "$BIN" serve --root "$ROOT" --duration "$mins" --compat-http --receive-dir /mnt/us/documents >>"$LOG" 2>&1 &
    launch_pid=$!
    printf '%s\n' "$launch_pid" >"$STARTPIDFILE"
    chmod 600 "$STARTPIDFILE" 2>/dev/null || true
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
        rm -f "$STARTPIDFILE"
        active_port="$(printf '%s\n' "$diag" | sed -n 's/^TCP_LISTEN=OK ://p' | sed -n '1p')"
        case "$active_port" in
            ''|*[!0-9]*) active_port=53317 ;;
        esac
        if [ "$mins" = "0" ]; then
            msg "LocalSend 已启动\n兼容接收模式 HTTP\n端口 $active_port"
        else
            msg "LocalSend 已启动\nHTTP 接收 ${mins} 分钟\n端口 $active_port"
        fi
    else
        stop_ok=1
        stop_daemon quiet || stop_ok=0
        if pid_is_daemon "$launch_pid"; then
            terminate_pid_strict "$launch_pid" || stop_ok=0
            firewall_cleanup_strict || stop_ok=0
        fi
        rm -f "$STARTPIDFILE"
        if [ "$stop_ok" -eq 1 ]; then
            msg "LocalSend 启动自检失败\n已确认停止并恢复防火墙\n请查看 logs/localsend.log"
        else
            msg "LocalSend 启动自检失败\n停止或防火墙恢复不完整\n请立即查看 logs/localsend.log"
        fi
        return 1
    fi
}

stop_daemon() {
    quiet="$1"
    pid="$(daemon_pid 2>/dev/null)"
    if [ -z "$pid" ]; then
        rm -f "$PIDFILE" "$ROOT/state/daemon.lock" "$STARTPIDFILE"
        if firewall_cleanup_strict; then
            [ "$quiet" = "quiet" ] || msg "LocalSend 当前未运行\n临时防火墙规则已确认清理"
            return 0
        fi
        [ "$quiet" = "quiet" ] || msg "LocalSend 当前未运行\n但防火墙清理或复核失败\n请查看 logs/localsend.log"
        return 1
    fi

    if ! terminate_pid_strict "$pid"; then
        firewall_cleanup_strict || true
        [ "$quiet" = "quiet" ] || msg "LocalSend 停止不完整\n进程仍存在 PID=$pid\n请查看 logs/localsend.log"
        return 1
    fi

    rm -f "$PIDFILE" "$ROOT/state/daemon.lock" "$STARTPIDFILE"
    if ! firewall_cleanup_strict; then
        [ "$quiet" = "quiet" ] || msg "LocalSend 进程已停止\n但防火墙清理或复核失败\n请查看 logs/localsend.log"
        return 1
    fi
    [ "$quiet" = "quiet" ] || msg "LocalSend 已确认停止\n临时防火墙规则已确认恢复"
    return 0
}

network_diagnostics() {
    ensure_log || { msg "无法创建 LocalSend 日志"; return 1; }
    result="$($BIN selftest --root "$ROOT" 2>&1)"
    printf '%s\n' "$result" >>"$LOG"
    msg "$result"
}

refresh_devices() {
    ensure_log || { msg "无法创建 LocalSend 日志"; return 1; }
    if ! running; then
        if ! start_daemon 10 >/dev/null 2>&1; then
            msg "设备刷新失败\nLocalSend 无法启动\n请查看 logs/localsend.log"
            return 1
        fi
    fi
    pid="$(daemon_pid 2>/dev/null)"
    if [ -z "$pid" ] || ! pid_is_daemon "$pid"; then
        msg "设备刷新失败\nLocalSend 进程不可用"
        return 1
    fi
    kill -USR1 "$pid" 2>/dev/null || {
        msg "设备刷新失败\n无法触发设备广播"
        return 1
    }
    sleep 3
    if ! "$BIN" menu --root "$ROOT" --write "$ROOT/menu.json" >>"$LOG" 2>&1; then
        msg "设备刷新失败\n无法更新 KUAL 菜单\n请查看日志"
        return 1
    fi
    count="$("$BIN" peers --root "$ROOT" --count 2>/dev/null | tr -cd '0-9')"
    msg "设备列表已刷新\n发现 ${count:-0} 台设备\n菜单将自动刷新"
}
send_outbox() {
    peer="$1"
    if [ ! -d "$OUTBOX" ]; then
        if ! mkdir -p "$OUTBOX"; then
            msg "无法创建 Outbox\n$OUTBOX"
            return 1
        fi
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


install_result_value() {
    key="$1"
    text="$2"
    printf '%s\n' "$text" | sed -n "s/^${key}=//p" | sed -n '1p'
}

html_escape() {
    # Result dialogs are generated from archive names/result text. Escape them
    # before embedding in Pillow HTML so a crafted filename cannot inject HTML.
    printf '%s' "$1" | sed \
        -e 's/&/\&amp;/g' \
        -e 's/</\&lt;/g' \
        -e 's/>/\&gt;/g' \
        -e 's/"/\&quot;/g' \
        -e "s/'/\\&#39;/g"
}

write_install_dialog_html() {
    kind="$1"
    version="$2"
    display="$3"
    line1="$4"
    line2="$5"
    line3="$6"

    mkdir -p "$ROOT/ui" 2>/dev/null || return 1
    chmod 755 "$ROOT/ui" 2>/dev/null || true
    html="$ROOT/ui/install-result.html"
    tmp="$ROOT/ui/.install-result.html.$$"

    h_version="$(html_escape "$version")"
    h_display="$(html_escape "$display")"
    h_line1="$(html_escape "$line1")"
    h_line2="$(html_escape "$line2")"
    h_line3="$(html_escape "$line3")"

    if [ "$kind" = "success" ]; then
        h_title="✓ ZIP 安装成功"
        h_mark="✓"
        h_class="success"
    else
        h_title="✕ ZIP 安装失败"
        h_mark="✕"
        h_class="failure"
    fi

    cat >"$tmp" <<EOF
<html lang="zh-CN" dir="ltr">
<head>
<meta charset="utf-8">
<script type="text/javascript" src="///usr/share/webkit-1.0/pillow/strings/sample_custom_dialog_strings.js"></script>
<script type="text/javascript" src="///usr/share/webkit-1.0/pillow/javascripts/local_debug.js"></script>
<script type="text/javascript" src="///usr/share/webkit-1.0/pillow/javascripts/constants.js"></script>
<script type="text/javascript" src="///usr/share/webkit-1.0/pillow/javascripts/pillow.js"></script>
<script type="text/javascript" src="///usr/share/webkit-1.0/pillow/javascripts/sample_custom_dialog.js"></script>
<script type="text/javascript" src="///usr/share/webkit-1.0/pillow/javascripts/window_title.js"></script>
<script>
var State = { width: Pillow.pointsToPixels(255), height: Pillow.pointsToPixels(258) };
try {
    var orientation = nativeBridge.getStringLipcProperty("com.lab126.winmgr", "orientation");
    if (orientation != "U") {
        State = { width: Pillow.pointsToPixels(345), height: Pillow.pointsToPixels(220) };
    }
} catch (e) {}
</script>
<style>
html, body { margin:0; padding:0; background:#fff; color:#000; font-family:sans-serif; }
.wrap { padding:18pt 18pt 14pt 18pt; text-align:center; }
.mark { font-size:34pt; font-weight:bold; line-height:38pt; }
.title { font-size:21pt; font-weight:bold; margin:0 0 8pt 0; }
.version { font-size:19pt; font-weight:bold; margin:4pt 0 8pt 0; }
.name { font-size:11pt; margin:0 0 10pt 0; white-space:nowrap; overflow:hidden; text-overflow:ellipsis; }
.detail { font-size:12pt; line-height:18pt; margin:2pt 0; }
.failure .title, .failure .mark { font-weight:bold; }
.bottomButton { margin:13pt 18pt 0 18pt; border:2px solid #000; padding:9pt 0; text-align:center; font-size:15pt; font-weight:bold; }
</style>
</head>
<body class="$h_class" onload="SampleCustomDialog.init();">
<div class="wrap">
  <div class="mark">$h_mark</div>
  <div class="title">$h_title</div>
  <div class="version">$h_version</div>
  <div class="name">$h_display</div>
  <div class="detail">$h_line1</div>
  <div class="detail">$h_line2</div>
  <div class="detail">$h_line3</div>
</div>
<div class="bottomButton" onclick="nativeBridge.dismissMe();">确定</div>
<!-- Pillow requires these standard IDs even when this custom layout does not use them. -->
<div style="display:none"><div id="buttonZeroText"></div><div id="searchEntry"></div><div id="title"></div><div id="text"></div></div>
</body>
</html>
EOF
    chmod 644 "$tmp" 2>/dev/null || true
    if ! mv -f "$tmp" "$html" 2>/dev/null; then
        rm -f "$tmp" 2>/dev/null || true
        return 1
    fi

    blank="$ROOT/ui/install-blank.html"
    blank_tmp="$ROOT/ui/.install-blank.html.$$"
    cat >"$blank_tmp" <<'EOF'
<html lang="zh-CN" dir="ltr">
<head>
<meta charset="utf-8">
<script type="text/javascript" src="///usr/share/webkit-1.0/pillow/strings/sample_custom_dialog_strings.js"></script>
<script type="text/javascript" src="///usr/share/webkit-1.0/pillow/javascripts/local_debug.js"></script>
<script type="text/javascript" src="///usr/share/webkit-1.0/pillow/javascripts/constants.js"></script>
<script type="text/javascript" src="///usr/share/webkit-1.0/pillow/javascripts/pillow.js"></script>
<script type="text/javascript" src="///usr/share/webkit-1.0/pillow/javascripts/sample_custom_dialog.js"></script>
<script type="text/javascript" src="///usr/share/webkit-1.0/pillow/javascripts/window_title.js"></script>
<script>var State={width:Pillow.pointsToPixels(255),height:Pillow.pointsToPixels(258)};</script>
</head>
<body onload="SampleCustomDialog.init();">
<div style="display:none"><div id="buttonZeroText"></div><div id="searchEntry"></div><div id="title"></div><div id="text"></div></div>
</body>
</html>
EOF
    chmod 644 "$blank_tmp" 2>/dev/null || true
    if ! mv -f "$blank_tmp" "$blank" 2>/dev/null; then
        rm -f "$blank_tmp" 2>/dev/null || true
        return 1
    fi
    return 0
}

mark_install_dialog_from_last() {
    [ "$1" = "success" ] || return 0
    last="$($BIN install-zip --root "$ROOT" --last 2>/dev/null)" || return 1
    last_at="$(install_result_value installed_at "$last")"
    last_name="$(install_result_value installed "$last")"
    [ -n "$last_at" ] && [ -n "$last_name" ] || return 1
    mkdir -p "$ROOT/state" 2>/dev/null || return 1
    chmod 700 "$ROOT/state" 2>/dev/null || true
    marker="$ROOT/state/last-install-dialog-at"
    marker_tmp="$ROOT/state/.last-install-dialog-at.$$"
    if printf '%s\t%s\n' "$last_at" "$last_name" >"$marker_tmp" 2>/dev/null; then
        chmod 600 "$marker_tmp" 2>/dev/null || true
        if mv -f "$marker_tmp" "$marker" 2>/dev/null; then
            return 0
        fi
    fi
    rm -f "$marker_tmp" 2>/dev/null || true
    return 1
}

install_dialog_marker_matches_last() {
    marker="$ROOT/state/last-install-dialog-at"
    [ -r "$marker" ] || return 1
    last="$($BIN install-zip --root "$ROOT" --last 2>/dev/null)" || return 1
    last_at="$(install_result_value installed_at "$last")"
    last_name="$(install_result_value installed "$last")"
    marker_at="$(cut -f1 "$marker" 2>/dev/null | sed -n '1p')"
    marker_name="$(cut -f2- "$marker" 2>/dev/null | sed -n '1p')"
    [ -n "$last_at" ] && [ "$marker_at" = "$last_at" ] && [ "$marker_name" = "$last_name" ]
}

show_install_dialog() {
    kind="$1"
    version="$2"
    display="$3"
    line1="$4"
    line2="$5"
    line3="$6"
    fallback="$7"

    # Always update KUAL's own status path as a first fallback. KUAL backgrounds
    # extension actions, so this alone is not sufficiently visible for a long
    # ZIP install; the Pillow modal below is the primary completion signal.
    msg "$fallback"

    if write_install_dialog_html "$kind" "$version" "$display" "$line1" "$line2" "$line3"; then
        if command -v lipc-set-prop >/dev/null 2>&1; then
            # customDialog is a DIALOG-layer Pillow surface and appears above
            # KUAL without stopping/restarting the framework. The relative path
            # form matches the Kindle customDialog loader used on Touch/PW/Voyage.
            # Match the proven KindleMenu sequence: clear the old Pillow
            # surface first, then open the actual result after a short delay.
            lipc-set-prop com.lab126.pillow customDialog \
                '{"name":"../../../../mnt/us/extensions/localsend/ui/install-blank"}' \
                >/dev/null 2>&1 || true
            if command -v usleep >/dev/null 2>&1; then
                usleep 250000 >/dev/null 2>&1 || true
            else
                sleep 1
            fi
            if lipc-set-prop com.lab126.pillow customDialog \
                '{"name":"../../../../mnt/us/extensions/localsend/ui/install-result","clientParams":{"dismiss":true}}' \
                >/dev/null 2>&1; then
                mark_install_dialog_from_last "$kind" >/dev/null 2>&1 || true
                return 0
            fi
        fi
    fi

    # Last-resort visible framebuffer hint. Do not clear/freeze the framework;
    # just stamp a short result line if Pillow is unavailable.
    if command -v eips >/dev/null 2>&1; then
        if [ "$kind" = "success" ]; then
            if eips 2 34 "ZIP OK: $version" >/dev/null 2>&1; then
                mark_install_dialog_from_last "$kind" >/dev/null 2>&1 || true
            fi
        else
            eips 2 34 "ZIP FAILED: $version" >/dev/null 2>&1 || true
        fi
    fi
    return 0
}

install_refresh() {
    mkdir -p "$ROOT/state" "$ROOT/logs" /mnt/us/documents
    ensure_log || { msg "无法创建 LocalSend 日志"; return 1; }
    if "$BIN" menu --root "$ROOT" --write "$ROOT/menu.json" >>"$LOG" 2>&1; then
        count="$($BIN install-zip --root "$ROOT" --receive-dir /mnt/us/documents --count 2>/dev/null | tr -cd '0-9')"
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
        version="$(install_result_value version "$result")"
        display="$(install_result_value display "$result")"
        size="$(install_result_value size "$result")"
        [ -n "$version" ] || version="未识别版本号"
        [ -n "$display" ] || display="$(install_result_value selected "$result")"
        msg "已选择待安装 ZIP\n版本：$version\n名称：$display\n大小：${size:-未知} bytes\n低空间直写：不复制、不备份、不回滚\n请确认后安装"
    else
        msg "选择 ZIP 失败\n$result\n请刷新 ZIP 列表后重试"
    fi
    return "$rc"
}

install_last() {
    result="$($BIN install-zip --root "$ROOT" --last 2>&1)"
    rc=$?
    if [ "$rc" -ne 0 ]; then
        msg "暂无成功安装记录"
        return "$rc"
    fi
    version="$(install_result_value version "$result")"
    display="$(install_result_value display "$result")"
    files="$(install_result_value files "$result")"
    replaced="$(install_result_value replaced "$result")"
    created="$(install_result_value created "$result")"
    source_zip="$(install_result_value source_zip "$result")"
    [ -n "$version" ] || version="未识别版本号"
    case "$source_zip" in
        deleted) source_note="已自动删除" ;;
        delete_failed) source_note="删除失败，已保留" ;;
        *) source_note="已保留" ;;
    esac
    msg "【上次 ZIP 安装成功】\n版本：$version\n名称：$display\n文件：${files:-0}（覆盖 ${replaced:-0} / 新增 ${created:-0}）\n源 ZIP：$source_note"
    return 0
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

current_receive_duration() {
    if ! running; then
        echo 0
        return 0
    fi
    pid="$(daemon_pid 2>/dev/null)"
    d=""
    if [ -r "/proc/$pid/cmdline" ]; then
        d="$(tr '\000' '\n' <"/proc/$pid/cmdline" 2>/dev/null | sed -n '/^--duration$/{n;p;q;}')"
    fi
    case "$d" in
        ''|*[!0-9]*) echo 0 ;;
        *) echo "$d" ;;
    esac
}

install_confirm() {
    delete_mode="${1:-keep}"
    case "$delete_mode" in
        keep|delete) ;;
        *) msg "安装参数无效"; return 1 ;;
    esac
    pending="$($BIN install-zip --root "$ROOT" --receive-dir /mnt/us/documents --pending 2>&1)"
    if [ "$?" -ne 0 ]; then
        msg "当前没有可确认的 ZIP\n请先刷新列表并选择一个 ZIP"
        return 1
    fi
    pending_version="$(install_result_value version "$pending")"
    pending_display="$(install_result_value display "$pending")"
    [ -n "$pending_version" ] || pending_version="未识别版本号"
    [ -n "$pending_display" ] || pending_display="$(install_result_value selected "$pending")"
    msg "ZIP 正在安装\n版本：$pending_version\n请勿断电或强制重启"

    was_running=0
    restart_duration=0
    if running; then
        was_running=1
        restart_duration="$(current_receive_duration)"
        if ! stop_daemon quiet; then
            msg "安装已取消\nLocalSend 无法确认停止\n未修改 Kindle 文件"
            return 1
        fi
    fi

    rotate_log
    printf '%s KUAL ZIP install requested: %s\n' "$(date '+%Y-%m-%d %H:%M:%S' 2>/dev/null)" "$pending" >>"$LOG"
    delete_arg=""
    [ "$delete_mode" = "delete" ] && delete_arg="--delete-after-success"
    result="$($BIN install-zip --root "$ROOT" --receive-dir /mnt/us/documents --dest-root /mnt/us --confirm $delete_arg 2>&1)"
    rc=$?

    restart_note=""
    allow_restart=1
    if [ "$rc" -ne 0 ]; then
        allow_restart=0
        restart_note="\n直写安装失败后可能已有部分文件被覆盖；为安全起见未自动重启 LocalSend"
    fi
    if [ "$was_running" = "1" ] && [ "$allow_restart" = "1" ]; then
        if start_daemon "$restart_duration" >/dev/null 2>&1; then
            if [ "$restart_duration" = "0" ]; then
                restart_note="\nLocalSend 已恢复持续接收"
            else
                restart_note="\nLocalSend 已恢复 ${restart_duration} 分钟接收模式"
            fi
        else
            restart_note="\nLocalSend 自动恢复失败，请手动启动"
        fi
    fi

    if [ "$rc" -eq 0 ]; then
        printf '%s ZIP install result:\n%s\n' "$(date '+%Y-%m-%d %H:%M:%S' 2>/dev/null)" "$result" >>"$LOG" 2>/dev/null || true
        version="$(install_result_value version "$result")"
        display="$(install_result_value display "$result")"
        files="$(install_result_value files "$result")"
        replaced="$(install_result_value replaced "$result")"
        created="$(install_result_value created "$result")"
        source_zip="$(install_result_value source_zip "$result")"
        [ -n "$version" ] || version="未识别版本号"
        [ -n "$display" ] || display="$(install_result_value installed "$result")"
        case "$source_zip" in
            deleted) delete_note="已自动删除" ;;
            delete_failed) delete_note="删除失败，已保留" ;;
            *) delete_note="已保留" ;;
        esac
        # Refresh the dynamic menu. v0.1.18's binary also uses this call to
        # display the Pillow modal once. This matters during the very first
        # self-upgrade from v0.1.17: the old KUAL shell is still running, but
        # $BIN now resolves to the freshly installed v0.1.18 binary.
        "$BIN" menu --root "$ROOT" --write "$ROOT/menu.json" >>"$LOG" 2>&1 || true
        restart_line="LocalSend：安装前未运行"
        if [ "$was_running" = "1" ]; then
            if running; then
                if [ "$restart_duration" = "0" ]; then
                    restart_line="LocalSend：已恢复持续接收"
                else
                    restart_line="LocalSend：已恢复 ${restart_duration} 分钟接收"
                fi
            else
                restart_line="LocalSend：自动恢复失败，请手动启动"
            fi
        fi
        if install_dialog_marker_matches_last; then
            # The freshly installed binary already opened the modal; keep only
            # a concise KUAL status fallback and avoid a duplicate dialog.
            msg "ZIP 安装成功\n版本：$version\n源 ZIP：$delete_note"
        else
            show_install_dialog \
                success "$version" "$display" \
                "文件：${files:-0}（覆盖 ${replaced:-0} / 新增 ${created:-0}）" \
                "源 ZIP：$delete_note" \
                "$restart_line" \
                "ZIP 安装成功\n版本：$version\n源 ZIP：$delete_note"
        fi
    else
        error_summary="$(printf '%s\n' "$result" | tail -n 1 | cut -c1-120)"
        [ -n "$error_summary" ] || error_summary="未知安装错误，请查看 logs/localsend.log"
        show_install_dialog \
            failure "$pending_version" "$pending_display" \
            "$error_summary" \
            "源 ZIP：已保留" \
            "LocalSend：未自动重启" \
            "ZIP 安装失败\n版本：$pending_version\n源 ZIP 已保留"
    fi
    return "$rc"
}

case "$1" in
    start)
        case "${2:-0}" in
            0|10) start_daemon "${2:-0}" ;;
            30) msg "接收 30 分钟选项已移除\n请使用持续接收或接收 10 分钟"; exit 1 ;;
            *) msg "不支持的接收时长\n请使用持续接收或接收 10 分钟"; exit 1 ;;
        esac
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
        install_confirm "$2"
        ;;
    install-cancel)
        install_cancel
        ;;
    install-last)
        install_last
        ;;
    help)
        msg "接收: HTTP v2.2\n保存: /mnt/us/documents\n核心: v0.1.11 全项目审计基线\n安装器: v0.1.18 低空间直写（安装完成原生弹窗/版本优先显示）\nMac framing: 保持 v0.1.7 实机成功逻辑\n支持安全安装接收的 ZIP 到 /mnt/us\n停止后会复核进程并恢复防火墙"
        ;;
    *)
        msg "LocalSend-KUAL v0.1.18\n未知命令: $1"
        exit 2
        ;;
esac
