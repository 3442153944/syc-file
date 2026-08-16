# 云梯 HarmonyOS 端

对标 Android/Windows/Go 后端的鸿蒙 NEXT 客户端（ArkTS + ArkUI）。详细架构见仓库根 `ARCHITECTURE.md` §3C。

---

## ⭐ 鸿蒙手机把文件写到公共 Download 目录的正确方法（血泪总结）

> 真机：华为 Mate 60 Pro / HarmonyOS 6（NEXT）。折腾了一整轮才定位到，这里一次记清，**以后别再踩**。

### 一、手机上这些全都行不通（官方设计如此，不是 bug）

| 方式 | 结果 | 原因 |
|---|---|---|
| `Environment.getUserDownloadDir()` | ❌ `code=801 The device doesn't support this api` | `SystemCapability.FileManagement.File.Environment.FolderObtain` **仅 2in1/平板**，手机无此能力 |
| `DocumentSelectMode.FOLDER`（选文件夹授权） | ❌ `errorcode -1` 空 uri | FolderSelection **仅 2in1/PC** |
| `${context.filesDir}/../Download/<bundle>` 相对路径 | ❌ 逃不出沙箱；主线程 `fs.mkdirSync` 还会触发沙箱校验 **卡死主线程 → APP_INPUT_BLOCK appfreeze** | 沙箱隔离 |
| `fs` 直接写 `/storage/Users/currentUser/Download/<bundle>` | ❌ 无权限 / ENOENT | 没有经过 picker 授权 |
| `request.agent` + `mode: BACKGROUND` + `saveas: './x'` | ⚠️ 落到应用沙箱 **cache**（`base/haps/<mod>/cache`），文件管理器看不到 | BACKGROUND 只能落沙箱 |
| `request.agent` 写 user file 但用 `mode: BACKGROUND` | ❌ `code=401 ... user file can only for Mode:FOREGROUND` | user file 必须 FOREGROUND |
| `request.agent` `saveas` 直接拼 docs uri（`file://docs/.../1.txt`） | ❌ `code=13400001 failed to open user file` | docs uri 不能这么拼给 request.agent |
| `fs.openSync(docsDirUri + '/1.txt', CREATE)` 直接拿目录 uri 拼子路径 | ❌ `code=13900002 no such file or directory` | **docs uri 必须先转成真实 path 才能给 fs** |

### 二、✅ 正确方案（QQ / `DownloadFileButton` 同款机制）

三步：**picker DOWNLOAD 模式拿授权目录 uri → `FileUri` 转真实 path → `fs` 直接读写**。
全程**无弹框**、任意文件类型、系统「文件管理 → Download → 应用名」可见、**绕开 801、不需要 request.agent**。

```typescript
import { picker, fileUri } from '@kit.CoreFileKit';
import fs from '@ohos.file.fs';
import { common } from '@kit.AbilityKit';

/** 在公共 Download/<bundleName> 目录下写文件（手机可见）。ctx 须为 UIAbilityContext。 */
async function writeToPublicDownload(ctx: common.UIAbilityContext): Promise<void> {
  // 1) picker DOWNLOAD 模式：无弹框直接返回 Download/<bundleName> 目录 uri
  const options = new picker.DocumentSaveOptions();
  options.newFileNames = ['1.txt'];
  options.pickerMode = picker.DocumentPickerMode.DOWNLOAD;   // ← 关键：直接下载模式，不弹选位置框
  const viewPicker = new picker.DocumentViewPicker(ctx);
  const result: string[] = await viewPicker.save(options);
  const dirUri: string = result[0];
  // dirUri = file://docs/storage/users/currentUser/download/<bundleName>  （是「目录」uri，不是文件）

  // 2) 用 FileUri 把 docs uri 转成真实文件系统 path（不转直接拼 fs 打不开！）
  const dirPath: string = new fileUri.FileUri(dirUri).path;
  // dirPath = /storage/users/currentUser/download/<bundleName>

  // 3) picker 已授权该目录，fs 直接读写即可
  const filePath: string = `${dirPath}/1.txt`;
  const f = fs.openSync(filePath, fs.OpenMode.READ_WRITE | fs.OpenMode.CREATE | fs.OpenMode.TRUNC);
  fs.writeSync(f.fd, '123');
  fs.closeSync(f);
}
```

### 三、关键注意点

1. **`pickerMode = DocumentPickerMode.DOWNLOAD` 是核心**：普通 `save()` 会弹框让用户选位置；DOWNLOAD 模式**不弹框**，直接返回 `Download/<bundleName>` 目录 uri。这也是 `DownloadFileButton` 安全组件内部封装的东西。
2. **返回的是目录 uri，不是文件 uri**：要自己在目录下拼文件名。
3. **必须 `new fileUri.FileUri(uri).path` 转真实 path**：docs uri 直接拼子路径喂给 `fs` 会 `13900002`。转成 path 后，因为 picker 已授权该目录，`fs` 的 mkdir/open(CREATE)/write/read/rename 全部可用。
4. **临时授权**：picker 给的是临时读写权，**应用退到后台后失效**。前台操作没问题；需要长期/后台复用要重新 `save()` 拿（持久化授权仅 2in1 生效）。因此**后台自动同步**若要落公共目录，需在前台拿到 path 后缓存复用，或退化到沙箱。
5. 若坚持用 `request.agent` 下载到公共目录：`mode` 必须 **FOREGROUND**，且 `saveas` 要用 picker 返回的合法 user file uri（不能自己拼 docs 目录 uri）。一般直接用上面的 fs 方案更简单。

### 四、落地

- `storage/CloudLadderStorage.ets`：封装上述「拿授权目录 path + 建 `同步/`、`下载/` 子目录」，path 缓存复用。
  `isPublicWritable()` = 已授权 **且** 在前台，即「此刻能不能直接写共享目录」。
- 下载：前台点文件 → 落 `Download/<bundleName>/下载`，文件管理器可见。
- 同步：download_only，前台拿到 path 后写入 `Download/<bundleName>/同步/<folder>`。

### 五、后台同步怎么绕开「授权失效」（沙盒暂存 + 前台归位）

同步靠 `BackgroundMode.DATA_TRANSFER` 长时任务（`ohos.permission.KEEP_BACKGROUND_RUNNING`）在后台保持 WS 与网络。
但注意点 4：退到后台后 picker 授权失效，**能下载却写不进共享目录**。于是：

| 阶段 | 落盘位置 |
| --- | --- |
| 前台（已授权） | 直接写 `Download/<bundleName>/同步/<folder>/…` |
| 后台（授权失效） | 写沙盒 `<filesDir>/sync_staging/<folder>/…`，并把「暂存文件 → 目标公共路径」记进 preferences 清单 |
| 回到前台 | `Ability.onForeground` 重新 `initCloudStorage()` 拿授权 → `flush()` 逐条 `copyFile` 搬进共享目录 → 清账 → 补报一轮 scan |

### 六、`relative_path` 含文件名（最容易搞错的一条协议约定）

服务端 `internal/sync/scan.go` 的 `relFromPath()` 算出的 `rel` 是**含文件名的完整相对路径**
（如 `sub/dir/a.png`），`task.RelativePath` 就是它；Android `SyncEngine.kt` 也是这么用的：

| 用途 | 正确做法 | 写错的后果 |
| --- | --- | --- |
| 本地落盘 | `File(localRoot, rel)`，即 `<同步/folder>/sub/dir/a.png` | 当成目录再拼 `file_name` → `.../a.png/a.png`，每个文件被套进一个同名目录，scan 上报出错，服务端无限重派 |
| 远端目录 | `joinRemote(folder.remote_path, rel.substringBeforeLast('/'))` | 直接用 `folder.remote_path` → 子目录文件去根目录找，服务端 `os.Stat` 落空，**稳定 404** |

`/file/download` 服务端做的是 `filepath.Join(path, name)`，所以 `path` 必须是**文件所在目录**、
`name` 必须是**叶子文件名**。注意 `pendingTasks` 的 REST 返回体里**没有 `remote_dir`**，
必须自己按上表算；只有 WS `task_created` 事件里的 `remote_dir` 是服务端算好的（`filepath.Dir`）。

鸿蒙端统一在 `SyncEngine.splitRel()` 拆成 `{dir, name}` 后再用，别在别处直接拼 `relative_path`。

##### 这个 bug 的残留会造成「永不收敛的重复下载」

写错的那版把每个文件落成了 `<folder>/a.png/a.png`（文件外面套一层同名目录）。
代码改对之后**磁盘上的残留还在**，于是形成闭环：

1. `walk()` 把 `a.png` 上报成**目录**（`file_hash` 为空）、`a.png/a.png` 上报成文件；
2. 服务端 `HandleScan` 比对 trunk：trunk 里 `a.png` 是文件且有 hash → 判定不一致 → **重派 download**；
   同时 `a.png/a.png` 不在 trunk → 再派一个 delete；
3. 客户端下载后写 `<folder>/a.png`，被那个同名目录挡住，`fs.open(CREATE)` 失败 → 落沙盒；
4. flush 的 `copyFile` 目标是同一路径，同样被挡 → 搬不走；
5. 下一轮 scan 回到第 1 步。流量哗哗跑，一个文件都落不了地。

排查提示：**`pendingTasks` 返回 0 不代表没任务**——worker 一派发就把 `sync_status`
从 `pending` 改成 `syncing`，积压全在 `syncing` 里，且 Reaper 会周期性重试超时的 syncing 任务。

自愈：`cloudLadderStorage.clearBlockingPath(path, wantDir)` 在三处兜底——
下载写文件前、mkdir 建目录前、staging flush 的 copyFile 前——
发现目标位置类型不符就整棵清掉（download_only 以服务端 trunk 为准）。
日志里会打 `清理类型不符的残留: <path>`。

#### 前提：长时任务必须真的申请到（踩过的坑）

以上全部建立在「后台还活着」之上。`BackgroundTaskHelper` 曾经有两个叠加 bug，导致 DATA_TRANSFER
长时任务**从来没申请成功过**，现象是「下载流量起来约 5 秒后连接直接断，同步不再继续」
（进程被系统冻结 → 在途 TCP 被掐 → 连 setTimeout 重连都不会跑）：

1. `publishKeepAliveNotification()` 被 `await` 在 `startBackgroundRunning` **前面**。
   应用从没调过 `requestEnableNotification`，`publish()` 直接抛 **1600004**，
   同一个 try 把异常吞掉 → 长时任务那行永远执行不到。
   → 顺序必须反过来：**先申请长时任务**，通知只是可选补充，失败无所谓。
2. `start()` 在 `onCreate` 调用。那时窗口还没起来、应用不算前台，
   `startBackgroundRunning` 被系统以 **9800005** 拒绝。
   → 入口挪到 `Ability.onForeground`，做成幂等的 `ensureStarted()`，每次回前台都补一次。

#### ⛔ 最容易漏的一条：`backgroundModes` 必须写在 module.json5 的 ability 里

长时任务要同时满足**三个**条件，缺一个 `startBackgroundRunning` 就必然失败：

```json5
// module.json5 → module.abilities[0]
"backgroundModes": ["dataTransfer"],   // ← ①最容易漏。不声明必失败，
                                       //    且跟通知授权、前后台时机毫无关系
```
```json5
// module.json5 → module.requestPermissions
{ "name": "ohos.permission.KEEP_BACKGROUND_RUNNING" }   // ← ②
```
③ **必须在应用前台时调** `startBackgroundRunning`。`onCreate` 阶段窗口还没起来，
系统以 **9800005** 拒绝——所以入口在 `Ability.onForeground`，做成幂等的 `ensureStarted()`。

运行时申请的模式必须是 `backgroundModes` 声明的**子集**。

排查：hilog 过滤 `云梯Boot`，必须看到 `BackgroundTask started (DATA_TRANSFER)`。
失败码：`9800005` 先查①再查③，`201` 查②。设置页「保活状态」也会显示失败码。

另需人工确认一次：**设置 → 应用 → 云梯 → 耗电管理 → 允许后台活动**。
被限制后台活动时，长时任务申请成功也会被回收。

#### 参考实现：ClashBox（鸿蒙版 Clash）实际怎么活着的

[xiaobaigroup/ClashBox](https://github.com/xiaobaigroup/ClashBox) 是个有用的对照：

- 它的 `module.json5` 声明了 `backgroundModes: ["dataTransfer","location","taskKeeping"]`
  —— 印证了①是硬性要求；
- 但它真正对用户暴露的保活开关叫「**后台运行 - 模拟画中画**」，
  `common/utils/PipManager.ets` 用 `PiPWindow.create()` + `setAutoStartEnabled(true)`，
  回桌面时自动拉起一个画中画窗口，**完全没有调 `backgroundTaskManager`**；
- README 直言「HarmonyOS NEXT 的后台调度机制尚不成熟，进程仍可能被系统关闭」，
  所以另外内置了**核心恢复**（被杀后自动重启核心）。

两点教训：
1. **别把 VPN 当保活手段**。ClashBox 的 `ClashVpnAbility`（`type: "vpn"`）是它的代理实现，
   跟后台存活是两码事。
2. **别用 `location` 长时任务当保活**。系统会常驻显示定位使用指示器，用户一眼看得见，
   不是个能长期用的方案；ClashBox 声明了定位权限，但保活靠的并不是它。
   真要在长时任务之外再加一层，**画中画（PiP）才是这条赛道的实际做法**——
   它是一个用户可见、可手动关掉的悬浮窗，比偷偷占用定位诚实得多。

#### ⛔ 绝对不要用「应用是否在前台」的布尔来决定能不能写

这是本项目栽得最狠的一次。曾经在 `CloudLadderStorage` 里维护一个 `foreground` 布尔，
由 `Ability.onForeground/onBackground` 驱动，写盘前先判它。结果：

**系统弹框（通知授权、定位授权、picker 自身）都会让 Ability 触发 `onBackground`。**
而 `onForeground` 里又并发 fire 了「申请保活（会弹框）」和「归位沙盒文件」两条链，
弹框把 `foreground` 打翻成 `false` → 人明明在前台，整轮下载全写进沙盒，
`flushStaging()` 也因同一个条件恒返回 0 → 现象是「下载量极大、沙盒堆满，Download 里一个文件都没有」。

正确做法：**别猜，直接写，写失败再退。**
- `ensureWritable()` 只管「有没有拿到授权目录 path」，没有就重取（30s 失败冷却），不看前后台；
- 真正能不能写，由 `fs.open` 的结果回答——成功即可写，抛错才降级到沙盒
  （`SyncEngine.attemptDownload` 的降级分支）。无竞态、自我修正。

配套的顺序约束：
- `onForeground` 里必须**串行**：共享目录 → 归位 → 追赶 → **最后**才碰保活。
  共享目录是用户看得见的东西，优先保证；保活失败最多影响后台。
- 会弹框的 location 降级档，只在用户显式打开设置页「强制保活」时才走
  （`ensureStarted(allowLocationFallback)`），默认静默只试 `dataTransfer`，不打扰前台使用。
- 每轮任务队列跑干后自动 flush 一次，不干等前后台切换。

#### 沙盒是中转，不是存储——任何时候留在里面都算 bug

留在沙盒里的文件对用户完全不可见，等于「下载了但没拿到」。所以归位不能只靠几个离散时机
碰运气（启动 / 回前台 / WS 连上 / 队列跑干），必须有人一直盯：

- **`kickFlush()`**：立刻试一次，没搬干净就自动挂上 5s 重试表（`FLUSH_RETRY_INTERVAL_MS`）。
  凡是往沙盒里放东西的地方（`executeDownload` 落 staged、`executeMkdir` 落 staged）
  都要在 record 之后立刻调它，别等队列跑干。
- **重试表自管**：沙盒清空自动停表，部分条目失败自动续表，`SyncEngine.stop()` 时停表。
- **搬空后 `purgeIfEmpty()`** 把暂存目录整棵删掉重建——只 unlink 文件会留一堆空目录残影。
- **首页「文件归位」卡片**（在「最近下载」上方）是这条链路的唯一可视窗口：
  `storage/FlushLog.ets` 把每条 落沙盒 / 已归位 / 归位失败 记成流水推到 AppStorage，
  搬运中是实时消息流（新消息自动滚到顶部），空闲时是可翻的历史；点标题栏立刻催一次搬运。
  流水存 200 条、防抖 2s 落盘（一次搬几百个文件时不能每条都写 preferences）。

- `storage/SyncStagingStore.ets`：暂存区 + 清单（清单持久化，进程被杀也不丢）。
- 暂存中的文件**必须一起上报 `/sync/scan`**（`SyncEngine.mergeStaged`），否则服务端按「本地缺失」重复派发下载。
- 反过来，公共目录读不到时 **禁止上报 scan**（`catchUpFolder` 直接 return）：全量清单不全 = 服务端重派整个 trunk。

#### ⭐ 清单以「基线台账」为准，不以磁盘遍历为准

现象：**每次切前台，服务端把所有文件重派一遍**（靠 hash 相同被跳过，白空转一轮）。
根因：`walk()` 的 `fs.listFile` 在 **picker 临时授权的公共目录**上并不可靠——
README 第二节只验证过这种路径的 mkdir/open/write/read/rename，**从没验证过枚举**。
枚举一失败，清单就是空的，`items: []` 照样发出去，服务端认为本地啥也没有 → 全量重派。

Android/桌面端直接遍历真实目录没这问题；鸿蒙必须像 Android 那样自己记账：

- `sync/SyncBaseStore.ets`（对标 Android `SyncBaseStore.kt`）：
  `folderId → (rel → {hash,size,mtime})`，持久化在沙盒 JSON + 公共目录镜像（见下一节），进程被杀也不丢。
- **每成功落一个文件就 `set()`**（下载完成时记；`alreadyHave` 跳过时回填，
  用于老版本升级上来台账为空的情况），**收到 delete 就 `remove()`**。
- `catchUpFolder` 的清单：**台账为权威**（逐条 `fs.access` 确认还在，不在就抹掉让服务端重派），
  磁盘遍历降级成补充手段——走通就并进来（捡外部塞进来的文件）并回填台账，走不通只记一笔，
  **不影响上报**。
- 兜底：清单为空且（台账非空 或 遍历失败）时**跳过本轮 scan**，绝不告诉服务端「我啥也没有」。

`fs.access` 在授权目录上是可靠的（单文件路径校验），`fs.listFile` 不可靠——这个差异是关键。

##### 台账存沙盒文件，不存 preferences

台账体量**随文件数线性增长**（几百个文件 × 相对路径 + 64 位 hash ≈ 上百 KB），
而 preferences 是给小配置项设计的，官方文档对 string value 上限说法都不一致
（8192 字节 / 16MB 两种都能查到）——塞进去既可能被截断，又要在每次防抖保存时重写整串。

所以凡是「体量随数据增长」的一律走 `storage/JsonFileStore`（沙盒普通文件 + 原子写）：

| 台账 | 文件 |
| --- | --- |
| 同步基线 | `<filesDir>/sync_baseline.json` |
| 沙盒暂存清单 | `<filesDir>/sync_staging.json` |
| 搬运流水 | `<filesDir>/sync_flush_log.json` |

写入走 `tmp + rename`：后台被冻结/杀进程是常态，中途挂掉最多留个 `.tmp`，
原文件始终完整；JSON 解析失败按「空台账」处理，下一轮同步重建，绝不用半截数据。
只有真正的小配置（服务器地址、开关、folder 映射）才继续留在 preferences。

#### ⭐⭐ 「一上线就全量拉一次」的根因：台账和数据不在同一块存储上

现象：**鸿蒙端每次上线，服务端把整个 folder 重派一遍**（`alreadyHave` 的 hash 比对拦下了真正的
下载，所以不烧流量，但每个文件都要重算一遍 blake3，还刷出一整屏任务记录）。

安卓为什么没这问题——两条，缺一不可：

| | Android | 鸿蒙（改之前） |
| --- | --- | --- |
| 清单来源 | **真实磁盘遍历**（`walk(localRoot)`），磁盘就是唯一真相 | 沙盒台账（`listFile` 在授权目录不可信，见上一节） |
| 台账位置 | `<ExternalStorage>/FileSync/sync_base.json`，**和同步数据同一块存储** | 沙盒 `filesDir`，**和数据（公共 Download/包名）是两块独立存储** |

安卓的台账只用于 Phase1 变更探测，丢了顶多多算几次 hash，清单照样完整；
鸿蒙的台账**就是清单本身**，而沙盒会被「重装 / 清除数据 / DevEco 每次装新调试包」单独清掉——
数据还在公共目录里躺着，台账却归零了 → 清单为空 → `HandleScan` 判定本设备什么都没有 → 全量重派。
靠 hash 校验兜住只是「下载前最后一道闸」，不是修复。

三处改动（`SyncBaseStore.ets` / `SyncEngine.ets`）：

1. **台账镜像与数据同生共死**：权威副本写到 `<Download/包名>/同步/.yt_baseline.json`，
   沙盒那份降级成「公共目录还没授权时也能用」的快速副本。`SyncEngine.ensureBaselineMirror()`
   在**每轮追赶的 scan 之前**调 `attachMirror()`：沙盒有账 → 补写镜像；沙盒空而镜像在 → 用镜像重建。
2. **空清单闸门 + 枚举自检**：每个 folder 根下放哨兵 `.ytsync`（`ensureSentinel`）。
   清单为空时先 `canTrustEmptyListing()`——`listFile` 连刚写进去的哨兵都列不出来，
   说明这个目录上的「空」是枚举坏了而不是真没文件，**本轮不上报**（宁可不报，绝不报空）。
   自检通过才认这个空，正常上报走全量补派（真·首次同步就走这条）。
   哨兵和镜像都在 `shouldIgnore`（`.yt` 前缀）里，不会进清单被服务端派 delete。
3. **强制落盘**：台账原本只有 1.5s 防抖保存，后台被冻结/杀掉就丢一截。新增 `flushNow()`，
   在**上报 scan 前 / `SyncEngine.stop()` / `Ability.onBackground`** 各刷一次。

用户侧出口：同步列表页「重新对齐」→ 二选一。「按台账对齐」走上面的闸门；
「强制全量重取」(`triggerCatchUp(true)`) 才允许上报空清单 = 显式告诉服务端「全发给我」。
**自动路径永远不会自己做这个决定。**

排查入口还是那行 `SCAN汇总`，现在多了两个字段：`遍历=N`（listFile 到底列出几项）
和 `镜像=在/无`。`台账=0 遍历=0 镜像=无` = 台账真丢了；`遍历=0` 而 `台账>0` = 枚举不可信。

#### ⛔ `walk()` 里绝对不许静默跳过

`HandleScan` 是**全量比对**：清单里少一个文件，服务端就认为本地缺这个文件，
`findItem` 返回 `!ok` → 重派 download。每轮 scan 都少同一批 → 每轮都重派同一批 →
流量哗哗跑、文件被反复重写（时间戳永远是"刚刚"）、永不收敛，**而且日志上一点痕迹都没有**。

曾经踩的写法：
```ts
try { names = await fs.listFile(dir); } catch (e) { return; }   // ❌ 整个子树没了
try { stat = await fs.stat(full); } catch (e) { continue; }     // ❌ 少一个
const hash = await this.cachedHash(...); if (hash.length === 0) continue;  // ❌ 少一个
```
现在三处全部改成 **throw**，由 `onConnected` 捕获并跳过本轮 scan（打 error 日志）。
宁可整轮不报、下一轮再来，也绝不上报残缺清单。

判据也别搞混：**哈希只算文件内容**（`fileHashHex(path)` 打开文件算 blake3），
`cachedHash` 的 `path|size|mtime` 只是**进程内缓存键**，路径不参与哈希计算。

诊断入口：`executeDownload` 开头的 `alreadyHave()` —— 本地已有同 hash 文件时直接
`completeTask` 并打 `重复派发：本地已有同 hash 文件`。大量出现这条 = 服务端在重复派发，
且**哈希与路径都是对的**（否则匹配不上），问题必然在 scan 清单少报，去看上面那三处。
