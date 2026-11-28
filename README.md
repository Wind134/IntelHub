# informationHub — 全天候多领域突发情报与历史归档平台

基于 **Golang + Pure-Go SQLite (WAL + CJK-FTS5) + Cloudflare Tunnel + Flutter (Windows 11 & Android/ColorOS 17 原生通知客户端) + Hermes 深度协同** 构建的私人实时突发情报与全周期历史复盘平台。

## 目录结构

- `server/` — Golang 单体中枢（采集、五层过滤、SimHash 去重、三槽位独立模型热切换、\$3/月预算熔断、REST/WSS/SSE API、FCM/飞书通知、Hermes 免依赖 CLI）
- `client/` — Flutter 现代化双端客户端（Windows 11 无边框高对比度三栏终端 + WinRT 原生 Toast / ColorOS 17 流体光感适配 + 后台保活守护 + GitHub Releases 自动检查更新）
- `configs/` — 信源模板 `config.example.yaml` 与 Hermes 协同技能 `hermes-skill/SKILL.md`
- `deploy/` — `systemd --user` 单元文件、Inno Setup Windows 安装包脚本 `windows-installer.iss`、备机哨兵与热备脚本、一键发布脚本
- `.github/workflows/` — 多端自动编译与 GitHub Release 流水线（支持推送 `v*` Tag 自动打出 Windows `.exe` 安装包、Android `arm64` APK 与 Linux 静态服务端）
- `hermes.md` — 完整工程架构与预算设计方案

## 快速上手

### 1. 一键部署服务端到主生产机
```powershell
powershell -ExecutionPolicy Bypass -File .\deploy\deploy-all.ps1 -RemoteHost <你的SSH别名> -MigrateHermes
```

### 2. 打版本并触发 GitHub Actions 全平台自动打包
```bash
git tag v1.0.0
git push origin v1.0.0
```
流水线会自动发布 GitHub Release 并附带：
- `informationHub-Setup-v1.0.0-win64.exe`（Windows 安装包）
- `informationHub-v1.0.0-win64-portable.zip`（Windows 免安装便携版）
- `informationHub-v1.0.0-android-arm64.apk`（Android 64 位安装包）
- `intel-hub-v1.0.0-linux-amd64`（Linux 静态单文件服务端）

### 3. 本地手动编译客户端与 Windows 安装包
```powershell
cd client
# 运行 Windows 桌面端
flutter run -d windows

# 编译 Windows Release + Inno Setup 安装包 (.exe)
flutter build windows --release --dart-define=APP_VERSION=1.0.0
iscc /DMyAppVersion=1.0.0 ..\deploy\windows-installer.iss

# 编译 Android 64 位 Release APK
flutter build apk --release --target-platform android-arm64 --dart-define=APP_VERSION=1.0.0
```
