# LocalSend-KUAL

面向 **Kindle Voyage + KUAL** 的轻量 LocalSend 兼容实现，让旧 Kindle 可以在局域网内与 **Windows、macOS、iOS / iPadOS** 等 LocalSend 客户端互传文件，而不需要在 Kindle 上运行 Flutter 图形界面。

> 当前版本：**v0.1.9**  
> 已经实机验证成功的 Windows/macOS 传输核心仍保持 **v0.1.7 字节级冻结**；v0.1.8/v0.1.9 只增加外围稳定性和工具能力。

## 功能

- 兼容 LocalSend v2.2 的设备发现、注册、准备上传、上传与取消流程。
- Windows v2.1 固定 `Content-Length` 上传兼容。
- macOS v2.2 流式上传兼容，包括缺少 `Content-Length` / `Transfer-Encoding` 的特殊 framing。
- 支持 iPhone / iPad 上的 LocalSend 客户端与 Kindle 互传文件。
- 文件直接接收到 `/mnt/us/documents`。
- 支持从 Kindle 的 Outbox 向已发现设备发送文件。
- KUAL 菜单控制：启动接收、定时接收、停止、发现设备、发送 Outbox、网络诊断。
- 运行时临时开放 `wlan0` 的 53317 端口；停止后自动清理防火墙规则。
- SHA-256 校验、会话/token/IP 校验、路径穿越保护与临时文件原子落盘。
- 单实例保护、崩溃残留清理、日志轮转、存储空间诊断和 peer 状态写入节流。
- 自签名设备身份，用于与 HTTPS LocalSend peer 通信时的 mTLS / fingerprint 校验。
- **安全安装通过 LocalSend 收到的 ZIP 更新包到 Kindle USB 根目录 `/mnt/us`。**

## 平台兼容性

项目主要针对 **Kindle Voyage 上的 KUAL 环境**。

已完成重点实机验证：

- Windows → Kindle；
- macOS → Kindle；
- Windows/macOS 交叉连续传输。

协议兼容支持：

- iOS / iPhone；
- iPadOS / iPad。

其它 Kindle 型号可能可以运行，但未做同等程度的实机验证。

当前接收服务默认使用：

- 协议：HTTP（局域网兼容模式）
- LocalSend 协议版本：2.2
- 端口：53317
- 网络接口：`wlan0`
- 接收目录：`/mnt/us/documents`
- Outbox：`/mnt/us/LocalSend/Outbox`

由于 Kindle 接收端使用 HTTP，请只在可信任的局域网中使用。不要把 53317 端口暴露到公网。

## 安装

发布包的目录结构：

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
5. Windows、macOS、iPhone 或 iPad 与 Kindle 保持在同一局域网，然后在 LocalSend 中选择 Kindle 发送文件。

收到的文件会直接保存到：

```text
/mnt/us/documents
```

## “停止 LocalSend”会做什么

v0.1.9 不会只删除 PID 或修改菜单状态。

停止流程：

1. 根据 `daemon.pid` 找到实际 `localsend-kindle` 进程；
2. 校验 `/proc/<pid>/cmdline`，避免误杀其它进程；
3. 先发送 `SIGTERM` 并等待正常退出；
4. 超时后才使用 `SIGKILL`；
5. 再次确认原 PID 已经不再是 LocalSend；
6. 清理 `daemon.pid` / `daemon.lock`；
7. 删除临时 `LSKUAL` 防火墙规则；
8. 只有上述停止流程确认完成后，KUAL 才显示“LocalSend 已确认停止”。

如果进程仍然存在或防火墙清理失败，会明确显示停止不完整，而不是报告假成功。

## 通过 LocalSend 安装 ZIP 更新包

v0.1.9 新增了一个独立的安全安装器。它**没有修改已冻结的 Windows/macOS 传输核心**。

这里的“Kindle 根目录”只表示 Kindle USB 用户分区：

```text
/mnt/us
```

**不会解压到 Linux 系统根目录 `/`，不会修改只读 rootfs。**

### 使用方法

1. 在 Windows、macOS、iPhone 或 iPad 的 LocalSend 中，把更新 ZIP 发送给 Kindle。
2. ZIP 会先作为普通文件保存到 `/mnt/us/documents`。
3. 打开 KUAL：

```text
LocalSend
└── 安装 ZIP 到 Kindle 根目录
    ├── 刷新 ZIP 列表
    ├── 选择 → 某个更新包.zip
    ├── 确认安装已选 ZIP
    └── 取消待安装 ZIP
```

4. 先点“刷新 ZIP 列表”。
5. 选择具体 ZIP 文件。
6. 此时**不会立刻安装**，只会锁定待安装文件并记录 SHA-256。
7. 再点“确认安装已选 ZIP”才执行安装。

如果 LocalSend 当时正在运行，安装器会先确认停止 LocalSend；安装结束后再尝试恢复原来的持续接收状态。

安装完成后，原 ZIP 文件不会自动删除。

### ZIP 安装安全限制

真正写入 `/mnt/us` 前，会完整预检整个 ZIP：

- 拒绝绝对路径；
- 拒绝 `../` 路径穿越；
- 拒绝反斜杠异常路径；
- 拒绝符号链接、设备文件和其它特殊文件；
- 拒绝重复/大小写冲突目标；
- 拒绝“文件同时又作为目录父级”的异常 ZIP；
- 拒绝 ZIP 覆盖它自己；
- 限制 ZIP 条目数量；
- 计算解压后总大小并检查可用空间；
- 保留额外 32 MiB 安全空间；
- 选择 ZIP 时记录 SHA-256，确认安装时再次计算并比对，防止选中后文件被替换；
- 禁止更新包覆盖 LocalSend 的设备私钥、证书、peer 状态、日志和用户 `settings.json`。

允许正常覆盖程序代码，例如：

```text
extensions/koreader/...
koreader/...
extensions/localsend/bin/...
```

因此既可以安装其它 KUAL/KOReader 更新，也可以用于后续 LocalSend-KUAL 自身升级，同时保留 LocalSend 的本地身份和用户配置。

### 覆盖与失败回滚

安装时不是直接把 ZIP 内容粗暴写到目标文件：

1. 已存在的目标文件先备份到临时事务目录；
2. ZIP 中的新文件先写入目标目录旁边的临时文件；
3. 写完并 `fsync` 后再 `rename` 替换；
4. ZIP CRC/大小异常会被检测；
5. 任意文件安装失败，已应用的文件会按逆序自动恢复；
6. 安装成功后自动删除临时备份。

这能避免普通解压工具在中途出错时留下半个损坏文件。

> 任何设备在“整个多文件升级事务进行到一半时突然断电”都无法做到绝对原子；本安装器已经做到单文件原子替换、事务备份和失败回滚。安装升级包时建议保持 Kindle 电量充足，不要在安装过程中拔 USB 或强制重启。

## 配置与隐私

`extension/localsend/config/settings.json` 不提交到 Git 仓库。程序首次运行时会自动生成安全默认配置。

运行时会在 Kindle 本地生成：

```text
extension/localsend/config/settings.json
extension/localsend/state/device.crt
extension/localsend/state/device.key
extension/localsend/state/http-fingerprint
extension/localsend/state/peers.json
extension/localsend/state/status.json
extension/localsend/state/daemon.pid
extension/localsend/state/daemon.lock
extension/localsend/state/pending-install.json
extension/localsend/state/install.lock
extension/localsend/logs/localsend.log
```

这些内容可能包含设备身份、PIN、局域网 peer 信息或运行日志，均不应提交到公开仓库。

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

## 已冻结的双平台核心

`FROZEN_CORE_SHA256.txt` 记录了完成实机双平台验证的核心源码 SHA-256。`go test ./...` 中的冻结校验会在这些核心文件被意外修改时失败。

冻结范围包括：

- 接收服务；
- macOS framing 兼容；
- Windows 固定长度路径；
- 发送客户端；
- 设备发现；
- 临时防火墙；
- TLS / HTTP 设备身份；
- 协议核心类型。

v0.1.9 的 ZIP 安装器、KUAL 停止复核和菜单功能都位于冻结核心之外。

## 与 LocalSend 的关系

本项目是面向 Kindle/KUAL 的第三方兼容实现，不是 LocalSend 官方客户端，也不隶属于 Amazon。

LocalSend 官方项目：<https://github.com/localsend/localsend>

## 许可证

本项目采用 **MIT License**，详见 [LICENSE](LICENSE)。

LocalSend 名称、上游项目以及其它第三方内容的权利归各自权利人所有。
