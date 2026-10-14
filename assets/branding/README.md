# 私有云应用图标

主图：`private-cloud-icon.png`。参考原 Android 应用图标，用内置 imagegen 重绘。

设计提示：保留云朵、青色双向同步箭头和文件页；使用清晰的深蓝轮廓、青色渐变及透明背景；去掉模糊黑影和文字，保证小尺寸可辨识。

`icon-manifest.json` 用于 Tauri 官方图标生成器。Android 自适应前景缩放为 75%，背景为 `#F0F8FC`，避免启动器圆形裁切。

生成双端资源：

```powershell
pnpm --dir filesync-desktop exec tauri icon assets/branding/icon-manifest.json -o filesync-desktop/src-tauri/icons
```

生成的 Android `mipmap-*` PNG 放入 `Android/app/src/main/res`，相同资源名不保留重复 WebP。Windows 使用 `filesync-desktop/src-tauri/icons` 中的 ICO 和 PNG。512 像素商店图标保存在 `sizes/512x512.png`。
