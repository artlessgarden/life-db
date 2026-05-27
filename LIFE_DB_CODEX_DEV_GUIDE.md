# Life DB 开发交接文档

## 0. 项目定位

Life DB 是一个面向个人使用的本地优先数据库系统。

当前目标：

- 电脑作为主节点，运行 Go 服务端和 SQLite 主库。
- Web 端运行在电脑上，用于大屏编辑、整理、查看。
- Android 原生端用于随身记录、觉知显示、离线缓存和自动同步。
- TUI 使用 Go Bubble Tea，运行在电脑终端。
- 局域网内尽量零配置使用；Android 通过 mDNS/NSD 自动发现电脑服务。

核心原则：

- SQLite 是主库，不再把 Markdown 当主库。
- Markdown 以后只作为导出格式。
- CRUD 通过 Go 服务端统一落库。
- Android 离线时先写本地 outbox，在线后推送到服务端。
- WebSocket 只用于实时通知其他端刷新，不直接承担 CRUD。

## 1. 仓库结构

```text
life-db/
├── server/              # Go 服务端、SQLite、WebSocket、mDNS、TUI
├── web/                 # React + Vite Web 客户端
└── android/Now/         # Android 原生 Compose 客户端
```

## 2. 服务端

路径：

```bash
cd life-db/server
```

启动：

```bash
go mod tidy
go run . serve
```

当前默认端口：

```text
8787
```

数据目录：

```text
~/.local/share/life-db
```

主库：

```text
~/.local/share/life-db/life.db
```

常用命令：

```bash
go run . serve

go run . tui

go run . add "测试记录"
```

服务端职责：

- 维护 SQLite 主库。
- 提供 HTTP CRUD API。
- 提供 WebSocket `/ws` 实时变更通知。
- 通过 mDNS 广播 `_life-db._tcp`，让 Android 自动发现。
- 提供 TUI 入口。

## 3. 数据模型

核心表是 `entries`。

字段语义：

```text
id                UUID，客户端生成
content           记录内容
created_at        创建时间，毫秒时间戳
updated_at        更新时间，毫秒时间戳
deleted_at        软删除时间，null 表示未删除
version           乐观锁版本号
source_device_id  来源设备
```

同步和冲突原则：

- 新增记录：直接插入。
- 编辑记录：客户端必须带当前 version。
- 服务端发现 version 不一致时返回冲突，避免覆盖新内容。
- 删除记录：软删除，不物理删除，防止离线端又把旧数据同步回来。
- Android 离线新增先进入本地 outbox，恢复连接后推送。

## 4. WebSocket 是干什么的

HTTP 负责真正 CRUD：

```text
GET /api/entries
POST /api/entries
PUT /api/entries/:id
DELETE /api/entries/:id
```

WebSocket 只负责通知：

```text
entries_changed
```

流程：

```text
Android 新增记录
↓
POST 到 Go 服务端
↓
服务端写 SQLite
↓
服务端通过 WebSocket 广播 entries_changed
↓
Web / Android / TUI 重新拉取或局部更新
```

局域网延迟低，所以 WebSocket 会让多端同步观感接近实时。

## 5. Web 端

路径：

```bash
cd life-db/web
```

安装依赖：

```bash
npm install
```

开发运行：

```bash
npm run dev
```

浏览器打开：

```text
http://localhost:5173
```

如果需要让手机浏览器访问 Web：

```bash
npm run dev -- --host 0.0.0.0
```

然后手机访问：

```text
http://电脑局域网IP:5173
```

Web 端目标：

- 与 Android 保持类似布局。
- 顶部日期/状态。
- 中间时间河。
- 底部输入框。
- 右键或长按菜单：编辑、删除。
- WebSocket 收到变更后刷新。

## 6. Android 端

路径：

```text
life-db/android/Now
```

Android Studio 打开这个目录，不要打开旧的 `now` 项目。

包名：

```text
com.xiang.lifedb
```

关键配置：

`gradle.properties` 必须有：

```properties
org.gradle.jvmargs=-Xmx2048m -Dfile.encoding=UTF-8
android.useAndroidX=true
android.enableJetifier=true
kotlin.code.style=official
```

`app/build.gradle.kts` 里 Java/Kotlin 版本要一致：

```kotlin
compileOptions {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}

kotlinOptions {
    jvmTarget = "17"
}
```

Android 端职责：

- Compose UI。
- 本地 Room/SQLite 缓存。
- 离线 outbox。
- NSD 自动发现电脑 Go 服务端。
- HTTP CRUD/同步。
- WebSocket 监听变更。
- 后续可继续加 Widget、悬浮球、背景图、透明度设置。

## 7. Android 打包 APK

### 7.1 debug APK，最快自用

在 Android 项目目录执行：

```bash
cd life-db/android/Now
./gradlew assembleDebug
```

APK 输出：

```text
app/build/outputs/apk/debug/app-debug.apk
```

安装到手机：

```bash
adb install -r app/build/outputs/apk/debug/app-debug.apk
```

debug 包大是正常的，因为包含调试信息、未压缩调试元数据和 Compose/AndroidX 依赖。

### 7.2 Android Studio 图形方式

```text
Build
→ Build App Bundle(s) / APK(s)
→ Build APK(s)
```

完成后点击右下角 `locate` 找到 APK。

### 7.3 release APK，长期自用

先生成签名文件，只需要做一次：

```bash
mkdir -p ~/.android-keys
keytool -genkeypair \
  -v \
  -keystore ~/.android-keys/life-db-release.jks \
  -alias life-db \
  -keyalg RSA \
  -keysize 2048 \
  -validity 10000
```

然后在 `life-db/android/Now` 新建 `keystore.properties`：

```properties
storeFile=/home/xiang/.android-keys/life-db-release.jks
storePassword=你的密码
keyAlias=life-db
keyPassword=你的密码
```

再在 `app/build.gradle.kts` 里加签名配置。示例：

```kotlin
import java.util.Properties

val keystoreProperties = Properties()
val keystorePropertiesFile = rootProject.file("keystore.properties")
if (keystorePropertiesFile.exists()) {
    keystoreProperties.load(keystorePropertiesFile.inputStream())
}

android {
    signingConfigs {
        create("release") {
            storeFile = file(keystoreProperties["storeFile"] as String)
            storePassword = keystoreProperties["storePassword"] as String
            keyAlias = keystoreProperties["keyAlias"] as String
            keyPassword = keystoreProperties["keyPassword"] as String
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            isShrinkResources = false
            signingConfig = signingConfigs.getByName("release")
        }
    }
}
```

构建：

```bash
./gradlew assembleRelease
```

输出：

```text
app/build/outputs/apk/release/app-release.apk
```

安装：

```bash
adb install -r app/build/outputs/apk/release/app-release.apk
```

注意：以后升级同一个 App，必须用同一个 keystore 签名，否则 Android 会拒绝覆盖安装。

## 8. 当前优先开发任务

### 8.1 稳定 Android 同步

- 确认 Android 能通过 NSD 发现 `_life-db._tcp`。
- 发现失败时允许手动输入服务器地址。
- Android 启动时执行：
  1. 读取本地缓存。
  2. 启动 NSD 搜索。
  3. 发现服务后 push outbox。
  4. pull 服务端 entries。
  5. 建立 WebSocket。

### 8.2 CRUD 完整性

- 新增。
- 编辑。
- 软删除。
- 长按菜单。
- version 冲突处理。

### 8.3 UI

- Android 背景图选择。
- Android 背景透明度。
- Web 端同布局。
- Android Widget。
- Android 悬浮球。

### 8.4 后续功能

- 图片附件。
- 语音记录。
- 文件附件。
- Markdown 导出。
- AI 整理。
- 时间线搜索。

## 9. 给 Codex CLI 的开发提示

请遵守这些原则：

1. 不要大重构。
2. 一次只改一个功能。
3. Android、Web、Go 服务端的数据模型必须保持一致。
4. 服务端 SQLite 是主库。
5. Android 离线数据只能通过 outbox 同步到服务端。
6. 删除必须软删除。
7. 编辑必须使用 version，避免覆盖冲突。
8. WebSocket 只做通知，不做主数据传输。
9. UI 保持极简：顶部状态，中间时间河，底部输入。
10. 新增功能前先写清楚验证方法。

## 10. 常用排错

### AndroidX 报错

确认 `gradle.properties` 在项目根目录，内容包含：

```properties
android.useAndroidX=true
android.enableJetifier=true
```

不要放到：

```text
gradle-wrapper.properties
local.properties
```

### JVM target 不一致

确认 `app/build.gradle.kts`：

```kotlin
compileOptions {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}

kotlinOptions {
    jvmTarget = "17"
}
```

### 服务端 SQLite 编译 warning

`github.com/mattn/go-sqlite3` 编译时可能有 C warning。只要服务端能启动，先忽略。

### 手机访问不到电脑服务

确认服务端监听：

```bash
ss -ltnp | grep 8787
```

确认电脑 IP：

```bash
ip -4 addr show | grep -oP '(?<=inet\s)\d+(\.\d+){3}' | grep -v '^127'
```

手机浏览器测试：

```text
http://电脑IP:8787/health
```

## 11. 当前版本的产品目标

短期目标不是做大而全知识库，而是：

```text
快速记录
本地优先
局域网实时同步
多端 CRUD
手机觉知入口
电脑整理主控
```

等这一条线稳定，再扩展图片、语音、文件、AI 和 Markdown 导出。
