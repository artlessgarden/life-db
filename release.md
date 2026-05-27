要打“长期自用、体积更小、可持续升级”的 APK，就打 签名 release APK。

下面按你的 Arch 环境直接来。

1. 生成签名文件，只做一次
mkdir -p ~/.android-keys

keytool -genkeypair \
  -v \
  -keystore ~/.android-keys/life-db-release.jks \
  -alias life-db \
  -keyalg RSA \
  -keysize 2048 \
  -validity 10000

密码自己记住。这个 .jks 以后不能丢，同一个 App 升级必须用同一个签名。

2. 在 Android 项目里建签名配置

进入项目：

cd life-db/android/Now

新建：

cat > keystore.properties <<'EOF'
storeFile=/home/xiang/.android-keys/life-db-release.jks
storePassword=你的密码
keyAlias=life-db
keyPassword=你的密码
EOF

把 你的密码 换成刚才设置的密码。

确保不要提交：

grep -n "keystore.properties" .gitignore

如果没有，加进去：

cat >> .gitignore <<'EOF'
keystore.properties
*.jks
*.keystore
EOF
3. 修改 app/build.gradle.kts

打开：

life-db/android/Now/app/build.gradle.kts

在文件顶部加：

import java.util.Properties

然后在 android { ... } 里面加签名和 release 配置。完整关键结构应类似这样：

import java.util.Properties

plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

val keystoreProperties = Properties()
val keystorePropertiesFile = rootProject.file("keystore.properties")

if (keystorePropertiesFile.exists()) {
    keystoreProperties.load(keystorePropertiesFile.inputStream())
}

android {
    namespace = "com.xiang.lifedb"
    compileSdk = 35

    defaultConfig {
        applicationId = "com.xiang.lifedb"
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "1.0"
    }

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
            signingConfig = signingConfigs.getByName("release")

            isMinifyEnabled = true
            isShrinkResources = true
            isDebuggable = false

            proguardFiles(
                getDefaultProguardFile("proguard-android-optimize.txt"),
                "proguard-rules.pro",
            )
        }

        debug {
            isDebuggable = true
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    kotlinOptions {
        jvmTarget = "17"
    }
}

如果你文件里已经有 dependencies { ... }，保留它，不要删。

4. 新建混淆规则文件

如果没有这个文件：

app/proguard-rules.pro

新建：

touch app/proguard-rules.pro

先空着也行。
如果 release 后运行崩，再针对性加 keep 规则。现在先最小化。

5. 打 release APK
./gradlew clean assembleRelease

生成位置：

app/build/outputs/apk/release/app-release.apk

查看大小：

ls -lh app/build/outputs/apk/release/app-release.apk

安装：

adb install -r app/build/outputs/apk/release/app-release.apk

如果你之前手机上装的是 debug 版，可能签名不同，升级会失败。先卸载旧版：

adb uninstall com.xiang.lifedb
adb install app/build/outputs/apk/release/app-release.apk

卸载会清掉 App 本地数据；服务端数据在电脑 SQLite，不受影响。

6. 以后升级

以后每次改代码后：

./gradlew assembleRelease
adb install -r app/build/outputs/apk/release/app-release.apk

需要升级版本号时改：

versionCode = 2
versionName = "1.1"

versionCode 每次正式升级递增。

7. 常见问题

如果打包时报：

Keystore was tampered with, or password was incorrect

密码错了，改 keystore.properties。

如果报：

Cannot recover key

通常是 keyPassword 错。

如果安装时报：

INSTALL_FAILED_UPDATE_INCOMPATIBLE

手机上已有同包名但签名不同的版本，先：

adb uninstall com.xiang.lifedb

再安装。

如果 release 运行崩，先临时关闭混淆测试：

isMinifyEnabled = false
isShrinkResources = false

确认是混淆问题后再加规则。