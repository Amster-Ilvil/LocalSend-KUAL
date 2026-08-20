package app

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

func truncateMenuLabel(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	if maxRunes < 4 || utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	r := []rune(s)
	return string(r[:maxRunes-1]) + "…"
}

func WriteKUALMenu(path string, peers []Peer) error {
	return WriteKUALMenuWithInstalls(path, peers, nil)
}

func WriteKUALMenuWithInstalls(path string, peers []Peer, installZIPs []InstallZIPCandidate) error {
	items := []any{
		map[string]any{"name": "状态", "priority": 1, "action": "./bin/kual.sh", "params": "status", "exitmenu": false},
		map[string]any{"name": "启动接收（持续）", "priority": 2, "action": "./bin/kual.sh", "params": "start 0", "exitmenu": false},
		map[string]any{"name": "接收 10 分钟", "priority": 3, "action": "./bin/kual.sh", "params": "start 10", "exitmenu": false},
		map[string]any{"name": "接收 30 分钟", "priority": 4, "action": "./bin/kual.sh", "params": "start 30", "exitmenu": false},
		map[string]any{"name": "停止 LocalSend", "priority": 5, "action": "./bin/kual.sh", "params": "stop", "exitmenu": false},
		map[string]any{"name": "发现 / 刷新设备", "priority": 6, "action": "./bin/kual.sh", "params": "refresh", "exitmenu": false, "refresh": true},
	}

	var sendItems []any
	for i, p := range peers {
		if i >= 6 {
			break
		}
		alias := strings.TrimSpace(p.Alias)
		if alias == "" {
			alias = p.IP
		}
		sendItems = append(sendItems, map[string]any{
			"name": fmt.Sprintf("发送 Outbox → %s", alias), "priority": i + 1,
			"action": "./bin/kual.sh", "params": "send " + p.Fingerprint, "exitmenu": false,
		})
	}
	if len(sendItems) == 0 {
		sendItems = append(sendItems, map[string]any{"name": "暂无设备（先点“发现 / 刷新设备”）", "priority": 1, "action": "./bin/kual.sh", "params": "refresh", "exitmenu": false, "refresh": true})
	}
	items = append(items, map[string]any{"name": "发送 Outbox", "priority": 7, "items": sendItems})

	installItems := []any{
		map[string]any{"name": "刷新 ZIP 列表", "priority": 1, "action": "./bin/kual.sh", "params": "install-refresh", "exitmenu": false, "refresh": true},
	}
	if len(installZIPs) == 0 {
		installItems = append(installItems, map[string]any{"name": "暂无 ZIP（发送后点刷新）", "priority": 2, "action": "./bin/kual.sh", "params": "install-refresh", "exitmenu": false, "refresh": true})
	} else {
		for i, z := range installZIPs {
			installItems = append(installItems, map[string]any{
				"name":     "选择 → " + truncateMenuLabel(z.Name, 42),
				"priority": 2 + i,
				"action":   "./bin/kual.sh",
				"params":   "install-select " + z.Token,
				"exitmenu": false,
			})
		}
	}
	installItems = append(installItems,
		map[string]any{"name": "确认安装已选 ZIP", "priority": 20, "action": "./bin/kual.sh", "params": "install-confirm", "exitmenu": false},
		map[string]any{"name": "取消待安装 ZIP", "priority": 21, "action": "./bin/kual.sh", "params": "install-cancel", "exitmenu": false},
	)
	items = append(items, map[string]any{"name": "安装 ZIP 到 Kindle 根目录", "priority": 8, "items": installItems})
	items = append(items, map[string]any{"name": "网络诊断", "priority": 9, "action": "./bin/kual.sh", "params": "diagnose", "exitmenu": false})
	items = append(items, map[string]any{"name": "使用说明", "priority": 10, "action": "./bin/kual.sh", "params": "help", "exitmenu": false})

	menu := map[string]any{"items": []any{map[string]any{"name": "LocalSend", "priority": 1, "items": items}}}
	b, err := json.MarshalIndent(menu, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}
