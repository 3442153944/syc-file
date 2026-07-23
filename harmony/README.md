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
- 下载：前台点文件 → 落 `Download/<bundleName>/下载`，文件管理器可见。
- 同步：download_only，前台拿到 path 后写入 `Download/<bundleName>/同步/<folder>`。
