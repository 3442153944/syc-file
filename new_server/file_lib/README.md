# filecore —— 分片上传 Rust 核心库

`new_server` 上传服务的热路径核心库：预分配、乱序分片定位写、blake3 分片校验、
Merkle 树根校验、整文件哈希。以 C ABI 静态库（`libfilecore.a`）形式由 Go 经 cgo 调用。

- **实现**：`src/lib.rs`（Rust，`crate-type = ["staticlib"]`）
- **C 头**：`filecore.h`
- **产物**：`lib/libfilecore.a`（由 `build.ps1` 生成打包）
- **Go 封装**：`../pkg/filecore/`（cgo）

---

## 一、整体方案

上传从「一次性 multipart 整传」改为「描述 → 分片 → 校验落盘」，全程可乱序、可并发、
可断点续传、可秒传。哈希统一用 **blake3**（本身即 Merkle 树，快且能定位坏块），
客户端（Tauri，同为 Rust）与服务端共用同一套哈希/树实现，保证两端逐字节一致。

### 流程

```
① init   前端提交描述信息（总大小/分片大小/分片数/每片叶子哈希/Merkle树根/整文件哈希）
         → 服务端校验描述自洽（叶子重算树根 == 提交树根）
         → 秒传查重（同 file_hash+大小已存在则本地复制落盘，直接完成）
         → 建会话 + 预分配临时文件（fc_preallocate，与目标同盘便于原子 rename）
         → 返回 upload_id + 缺失分片列表

② chunk  乱序/并发上传每个分片（raw body），带 upload_id + index
         → fc_chunk_write：blake3 早校验（== 该片叶子哈希）→ 按 offset=index*chunk_size 定位写
         → 校验不过则拒绝且不置位，客户端重传该片
         → Redis 位图 SETBIT 置位

③ complete  收齐后（步骤4，待实现）
         → fc_finalize：单趟顺序读，重算每片叶子并逐块比对 → 重建 Merkle 树根与提交树根比对
           → 顺带算出整文件 blake3（秒传/去重键）
         → fc_move 原子落盘 → 写 file 表 / upload_history → 同步目录则派发同步任务

④ status  GET 查缺失分片，供断点续传
```

### 会话状态 = Redis + 磁盘临时文件

- **会话元数据 + 已收分片位图** 存 Redis（`pkg/upload_store`），**临时文件** 在磁盘。
  两者都独立于 Go 进程，**服务重启后仍可续传**（前提是 Redis 有持久化）。
- 会话 id = `sha256(userID | 目标路径 | file_hash | total_size | chunk_size)`，确定性派生。
  - **客户端文件变了** → `file_hash` 变 → id 变 → 天然开新会话，**绝不会误续到旧字节**。
  - **树对不上** → init 进门就用叶子重算树根比对，不一致直接拒绝让客户端整份重传。
- Redis 被清空 → 会话丢失，客户端重新 init 即整传。这是既定取舍，可接受。

---

## 二、filecore 库设计

**无状态**：不持有长生命周期会话句柄，会话/位图状态全在 Go+Redis。好处：抗服务重启、
无句柄泄漏、天然并发安全。每次 `fc_chunk_write` 各自开独立文件句柄 seek 写，
不相交区域的定位写并发安全。

### FFI 接口（见 `filecore.h`）

| 函数 | 作用 |
|---|---|
| `fc_abi_version()` | ABI 版本，链接自检 |
| `fc_preallocate(path, total_size)` | 预分配临时文件到 total_size（已存在只调长度，续传复用） |
| `fc_chunk_write(path, offset, data, len, expected_leaf)` | blake3 早校验 + 按 offset 定位写 |
| `fc_hash_chunk(data, len, out32)` | 算一段数据的 blake3 叶子哈希（客户端也用） |
| `fc_merkle_root(leaves, count, out32)` | 由叶子哈希数组构造 Merkle 树根（双端共用） |
| `fc_finalize(path, chunk_size, total_size, expected_leaves, leaf_count, expected_root, out_file_hash32, out_bad_index)` | 单趟校验：逐块比对 + 树根比对 + 算整文件哈希，定位坏块 |
| `fc_move(src, dst)` | 原子落盘：mkdir -p + rename，跨卷退化 copy+remove |

返回码：`FC_OK=0`；`FC_ERR_IO=-1`、`FC_ERR_ARG=-2`、`FC_ERR_LEAF_MISMATCH=-3`、
`FC_ERR_ROOT_MISMATCH=-4`。所有哈希均为 **32 字节 blake3**。

### Merkle 树构造规则（客户端必须一致）

叶子 = `blake3(分片字节)`；两两合并 `parent = blake3(left ‖ right)`；某层节点数为奇数时，
最后一个节点**原样进位**到上层。空 → `blake3("")`；单叶子 → 该叶子本身。
双端都调用 `fc_merkle_root` 即可保证一致。

---

## 三、构建

### 工具链要求（Windows）

cgo 在 Windows 上**必须用 mingw-w64 gcc，不能用 MSVC**；且为避免 C 运行时不匹配，
Rust 也走 **GNU ABI**（`x86_64-pc-windows-gnu`，链接 MSVCRT），对应 **MSVCRT** 版 mingw。

```powershell
# 1) mingw-w64 gcc（MSVCRT 版，与 rust gnu 目标的 ABI 匹配）
winget install --id BrechtSanders.WinLibs.POSIX.MSVCRT
# 2) Rust GNU 目标
rustup target add x86_64-pc-windows-gnu
```

> gcc 装在用户 PATH，新终端若找不到，刷新一次：
> ```powershell
> $env:Path = [Environment]::GetEnvironmentVariable("Path","Machine") + ";" +
>             [Environment]::GetEnvironmentVariable("Path","User")
> ```

### 已验证工具链版本（本项目实测通过）

| 组件 | 版本 / 值 | 说明 |
|---|---|---|
| Rust（cargo / rustc） | 1.88.0 | 默认 host `x86_64-pc-windows-msvc`，另加 gnu 目标 |
| Rust 目标 | `x86_64-pc-windows-gnu` | cgo 静态库走 GNU ABI，链接 MSVCRT |
| mingw-w64 gcc | 16.1.0（`x86_64-msvcrt-posix-seh`, r3） | winget 包 `BrechtSanders.WinLibs.POSIX.MSVCRT`（WinLibs） |
| Go | 1.25.3 | 可执行在 `C:\Program Files\Go\bin\go.exe`（不在默认 PATH） |
| cgo 开关 | `CGO_ENABLED=1`, `CC=gcc` | 默认 `CGO_ENABLED=0` 会导致 `build constraints exclude all Go files` |
| blake3 crate | 1.8.5 | 见 `Cargo.toml`，纯 Rust SIMD，无需 C 编译器 |
| native-static-libs | `-lkernel32 -lntdll -luserenv -lws2_32 -ldbghelp` | cgo LDFLAGS 需覆盖，由 `build.ps1` 末尾打印 |

> 客户端侧哈希实现（须与本库 blake3 逐字节一致）：
> - **桌面（Tauri, Rust）**：直接复用本 crate 的 `fc_hash_chunk` / `fc_merkle_root`。
> - **安卓（Kotlin）**：`io.github.rctcwyvrn:blake3:1.3`（纯 Java；KotlinCrypto 无 blake3、
>   trancee 需 JDK21 且 KMP 变体不便，均已放弃）。Merkle 树规则需在 Kotlin 侧照抄本库。

### 构建库

```powershell
# 在 file_lib 目录下
./build.ps1
```

`build.ps1` 会 `cargo build --release --target x86_64-pc-windows-gnu`，把
`target/.../release/libfilecore.a` 拷到 `lib/libfilecore.a`，并打印 `native-static-libs`
（cgo LDFLAGS 需覆盖的系统库）。

### 编译 Go（链接本库）

```powershell
$env:CGO_ENABLED = "1"; $env:CC = "gcc"
go build ./...        # 或 go test ./pkg/filecore/
```

cgo 指令写在 `../pkg/filecore/filecore.go`：

```go
// #cgo CFLAGS: -I${SRCDIR}/../../file_lib
// #cgo LDFLAGS: -L${SRCDIR}/../../file_lib/lib -lfilecore -lkernel32 -lntdll -luserenv -lws2_32 -ldbghelp
```

**先 `build.ps1` 生成 `.a`，再 `go build`。** 整个 `new_server` 已是 cgo 项目，
构建必须 `CGO_ENABLED=1` 且 gcc 在 PATH。

---

## 四、注意事项

- **LDFLAGS 需与 `native-static-libs` 同步**：Rust 静态库依赖的系统库由
  `build.ps1` 末尾打印（当前为 `-lkernel32 -lntdll -luserenv -lws2_32 -ldbghelp`）。
  升级 Rust/依赖后若链接报未定义符号，按新输出更新 `filecore.go` 的 LDFLAGS。
- **临时文件必须与目标同盘**：`fc_move` 优先 `rename`（同卷才原子且零拷贝），跨卷会退化
  为 copy+remove。Go 侧 `tempPathFor` 已把临时文件放到目标盘的 `BasePath/TempPath` 下。
- **Redis 位图是 MSB-first**：`SETBIT offset` 的 offset=0 对应字节最高位。Go 侧
  `MissingChunks` 的位序与之严格对齐，改动需保持一致（有单测覆盖）。
- **cgo 指针规则**：Go 把分片 `[]byte` 首地址传给 Rust，Rust 在调用期内使用、不留存，
  符合 cgo 规则；不要让 Rust 侧缓存这些指针跨调用。
- **`panic = "abort"`**：Rust 侧 panic 直接 abort 进程（不跨 FFI 展开）。所有对外函数
  都用返回码报错、不 panic；新增函数务必遵循，避免把 Go 进程带崩。
- **blake3 树规则双端一致**：改 Merkle 构造或分片语义时，客户端（Tauri）与服务端必须
  同步，否则树根对不上会触发整份重传。
- **go 可执行**：本机在 `C:\Program Files\Go\bin\go.exe`，不在默认 PATH。

---

## 五、目录

```
file_lib/
├── Cargo.toml          # crate 配置（staticlib, blake3, lto, panic=abort）
├── src/lib.rs          # 核心实现
├── filecore.h          # C ABI 声明
├── build.ps1           # 构建并打包到 lib/
├── lib/libfilecore.a   # 构建产物（Go cgo 链接目标）
├── target/             # cargo 中间产物（.gitignore）
└── README.md           # 本文件
```
