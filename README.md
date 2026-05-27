# Life DB

面向个人使用的本地优先 Life Database 原型：

- 电脑 Go 服务端：SQLite 主库、HTTP CRUD、WebSocket 实时广播、mDNS 局域网发现、Bubble Tea TUI。
- Web：React/Vite，和安卓同样的时间河布局，支持新增、长按/右键编辑和删除。
- Android：Kotlin/Compose 原生端，本地 SQLite 离线 outbox，NSD 自动发现电脑，HTTP 同步，WebSocket 触发刷新，支持背景图片和透明度。

## 目录

```text
life-db/
├── server/      Go 服务端 + CLI/TUI
├── web/         React/Vite Web UI
└── android/Now  Android Studio 项目
```

## 1. 启动电脑主库

```bash
cd server
go mod tidy
go run . serve
```

默认数据目录：

```text
~/.local/share/life-db/life.db
```

默认端口：

```text
8787
```

服务端会广播：

```text
_life-db._tcp.local
```

Android 会自动发现它。

## 2. 启动 Web

另开终端：

```bash
cd web
npm install
npm run dev
```

打开：

```text
http://localhost:5173
```

Web 通过 Vite proxy 连接本机 `127.0.0.1:8787`。

## 3. TUI

服务端运行后：

```bash
cd server
go run . tui
```

快捷键：

```text
enter 添加
r 刷新
↑/↓ 选择
d 删除
q 退出
```

也可以直接添加：

```bash
go run . add "关屏，去晒衣服"
```

## 4. Android

用 Android Studio 打开：

```text
android/Now
```

运行到模拟器或真机。Android 会通过 NSD/mDNS 自动发现电脑上的 Go 服务端。

如果模拟器发现不到 mDNS，可先用 Web/真机测；模拟器网络和 mDNS 有时不稳定。

## 5. 同步与冲突策略

当前是“电脑主库 + 移动端离线缓存”：

- Web/TUI 直接写电脑主库。
- Android 写本地 SQLite，并记录 outbox。
- 发现电脑后，Android 把 outbox 发给 `/api/sync`，服务端合并并返回完整 entries。
- 服务端写入后通过 WebSocket 广播，在线 Web/Android 立即刷新。

CRUD 冲突策略：

- 新增：永远合并。
- 编辑/删除：基于 `version` 做乐观锁。
- 如果 Android 离线编辑时电脑也改了同一条，服务端返回 conflict；当前 Android 以服务器为准刷新。
- 删除是软删除，避免离线端把已删记录重新带回来。

## 6. Markdown 导出

Markdown 不再是主库，只作为导出：

```bash
cd server
go run . export-md --out /home/xiang/Drafts/memo
```

会生成：

```text
/home/xiang/Drafts/memo/YYYY/MM/YYYY-MM-DD.md
```

## 7. 后续扩展位

推荐继续加：

- assets 表 + assets/ 文件目录：图片、语音、文件。
- Android Widget / 悬浮球复用本地 SQLite 最新记录。
- Web 搜索、按天过滤、标签。
- TUI 编辑功能增强。
- Android 后台服务持续同步。
