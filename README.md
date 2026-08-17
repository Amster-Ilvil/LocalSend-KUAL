# LocalSend-KUAL

面向 **Kindle Voyage + KUAL** 的轻量 LocalSend 兼容实现，让旧 Kindle 可以在局域网内与 Windows、macOS、iOS/iPadOS 等 LocalSend 客户端互传文件，而不需要在 Kindle 上运行 Flutter 图形界面。

> 当前版本：**v0.1.8**  
> v0.1.8 是稳定性加固层；已验证成功的 Windows/macOS 传输核心保持 v0.1.7 字节级冻结。

## 功能

- 兼容 LocalSend v2.2 的设备发现、注册、准备上传、上传与取消流程。
- Windows v2.1 固定 `Content-Length` 上传兼容。
- macOS v2.2 流式上传兼容，包括缺少 `Content-Length` / `Transfer-Encoding` 的特殊 framing。
- 支持与 iPhone / iPad 上的 LocalSend 客户端互传文件（iOS / iPadOS，按标准 LocalSend v2 协议兼容）。
- 文件直接接收到 `/mnt/us/documents`。
- 支持从 Kindle 的 Outbox 向已发现设备发送文件。
- KUAL 菜单控制：启动接收、定时接收、停止、发现设备、发送 Outbox、网络诊断。
- 运行时临时开放 `wlan0` 的 53317 端口；停止后自动清理防火墙规则。
- SHA-256 校验、会话/token/IP 校验、路径穿越保护与临时文件原子落盘。
- 单实例保护、崩溃残留清理、日志轮转、存储空间诊断和 peer 状态写入节流。
- 自签名设备身份，用于与 HTTPS LocalSend peer 通信时的 mTLS / fingerprint 校验。

## 兼容性说明

项目主要针对 **Kindle Voyage 上的 KUAL 环境**。其它 Kindle 型号可能可以运行，但未做同等程度的实机验证。

Windows 与 macOS 已完成实际 Voyage 双平台验证。iPhone / iPad 上的 LocalSend 客户端使用相同的 LocalSend v2 协议，因此也支持 iOS / iPadOS 端互传；目前尚未做与 Windows/macOS 同等程度的 Voyage 实机回归测试。

当前接收服务默认使用：

- 协议：HTTP（局域网兼容模式）
- LocalSend 协议版本：2.2
- 端口：53317
- 网络接口：`wlan0`
- 接收目录：`/mnt/us/documents`
- Outbox：`/mnt/us/LocalSend/Outbox`

由于 Kindle 接收端使用 HTTP，请只在可信任的局域网中使用。不要把 53317 端口暴露到公网。

## 安装

发布包的目录结构应为：

```text
extensions/
└── localsend/
    ├── bin/
    │   ├── kual.sh
    │   └── localsend-kindle
    ├── config.xml
    └── menu.json
```

安装步骤：

1. 如果旧版本正在运行，在 KUAL 中选择 `LocalSend → 停止 LocalSend`。
2. 将发布 ZIP 解压到 Kindle USB 根目录并覆盖扩展文件。
3. 断开 USB 后打开 KUAL。
4. 选择 `LocalSend → 接收 10 分钟`、`接收 30 分钟`或持续接收。
5. Windows、macOS、iPhone 或 iPad 与 Kindle 保持在同一局域网，然后在 LocalSend 中选择 Kindle 设备发送文件。

收到的文件会直接保存到：

```text
/mnt/us/documents
```


## 构建

需要 Go 1.23 或兼容版本。

运行测试：

```bash
go test ./...
go vet ./...
```

构建 Voyage ARMv7 静态二进制：

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
  go build -trimpath -ldflags='-s -w' \
  -o extension/localsend/bin/localsend-kindle \
  ./cmd/localsend-kindle
```

生成的运行包不需要 Go、动态库或源码。




## 与 LocalSend 的关系

本项目是面向 Kindle/KUAL 的第三方兼容实现，不是 LocalSend 官方客户端，也不隶属于 Amazon。

LocalSend 官方项目：<https://github.com/localsend/localsend>

## 许可证

本项目采用 **MIT License**，详见 [LICENSE](LICENSE)。

LocalSend 名称、上游项目以及其它第三方内容的权利归各自权利人所有。
