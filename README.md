# 🗂️ 云梯 (FileSync) — 私有云文件同步系统

## 📖 项目简介

**个人自托管的多端文件同步 / 传输平台**：Windows·Linux 桌面端、Android、HarmonyOS NEXT 三类客户端 + Go 后端，数据全部保存在自己的服务器上。
详细架构、模块与已知问题见 [ARCHITECTURE.md](ARCHITECTURE.md)。

---

## 🚀 核心功能

### 🔄 文件同步与传输
- **双向实时同步**：桌面端、Android 监听本地变更并上报；鸿蒙端为仅下行（download_only）。
- **冲突保留两者**：基于 base hash 的 CAS 检测，冲突副本隔离，可在客户端选择保留哪一方。
- **离线追赶**：重连后自动做全量清单比对，只补差异。
- **分片上传**：blake3 + Merkle 校验，支持并发、断点续传、秒传；四端共用同一份 Rust 核心实现。
- **版本历史**：文件版本查看与回滚。

### 📂 文件管理与分享
- 远端文件浏览 / 下载（Range 断点续传）、下载历史。
- **分享链接**（带过期）与**快传**（粘贴快传）。
- **Android 在线预览**：图片、视频、音频、PDF、文本、Office。
- **剪贴板同步**（桌面端）：多设备间推送剪贴板内容。

### 🧠 监控与管理
- **服务器监控**：CPU / 内存 / 磁盘 / 网络、进程与端口、资源告警，WebSocket 实时刷新。
- **管理后台**：用户、设备、操作日志、存储配额、角色权限。
- **应用内更新**：后端发布版本，客户端检查 / 推送，支持强制更新。
- **灾备**：桌面端支持多节点健康探测与自动切换。

---

## ⚙️ 技术栈

| 模块 | 技术实现 | 说明 |
| :--- | :--- | :--- |
| **后端服务** | **Go** `1.25` · Gin `1.12` · GORM · gorilla/websocket | API、WebSocket、同步引擎 |
| **核心库** | **Rust** `file_lib`（静态库，cgo 调用） | blake3 校验 / 定位写 / Merkle；同一份代码经原生 / JNI / NAPI 供各端复用 |
| **数据库** | **MySQL** `8.0` | 业务数据，GORM `AutoMigrate` 维护表结构 |
| **缓存/队列** | **Redis** `8.x` | 在线设备、同步队列与锁、上传会话、剪贴板历史 |
| **部署** | Linux（Ubuntu）· systemd · NGINX `1.28` · frp / Cloudflare Tunnel | 后端内置 supervisor 托管 frpc / ddns-go |
| **桌面端** | **Tauri 2** + **Rust**（reqwest / tokio-tungstenite / notify）· **Vue 3** + TypeScript + Vite + **Naive UI** + Pinia | Windows 为主，Linux 可构建；前端亦可 Web 模式运行 |
| **Android 端** | **Kotlin** `2.4` · Jetpack Compose (Material3) · OkHttp `5.3` · kotlinx-serialization | `minSdk 26` / `targetSdk 37`；Rust JNI 核心 |
| **HarmonyOS 端** | **ArkTS** + **ArkUI**（HarmonyOS NEXT，API 22） | Rust NAPI 核心；仅 arm64-v8a / x86_64 |

---

## 🧩 架构概览

```text
      ┌─────────────┬──────────────────┬───────────────┐
      │ 桌面端       │ Android          │ HarmonyOS      │
      │ Tauri+Vue3  │ Kotlin+Compose   │ ArkTS+ArkUI    │
      └──────┬──────┴────────┬─────────┴───────┬───────┘
             │  HTTP /v1 (Token + Device-Id)   │
             │  WebSocket (事件 / 同步任务)      │
             └───────────────┬─────────────────┘
                      ┌──────▼───────┐
                      │    NGINX     │  frp / Cloudflare Tunnel
                      └──────┬───────┘
                      ┌──────▼───────┐
                      │  Go Server   │  API · 同步引擎 · WS Hub
                      │  (+ file_lib)│  admin · monitor · update
                      ├──────────────┤
                      │ MySQL │ Redis│
                      └──────┬───────┘
                      本地磁盘（allowed_paths）
```

---

## 📁 目录

| 目录 | 内容 |
| :--- | :--- |
| `new_server/` | Go 后端（含 `file_lib/` Rust 核心、`SYNC_PROTOCOL.md` 同步契约、`deploy/` 部署模板） |
| `filesync-desktop/` | 桌面端（Tauri + Vue） |
| `Android/` | Android 客户端（含 `filecore_jni/`） |
| `harmony/` | 鸿蒙客户端 |

> 其余目录（`server/` 早期版本后端、`rootinstaller/` 等）不属于当前主线，现行后端是 `new_server/`。
