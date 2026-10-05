// previewUtil/PreviewType.kt
// 职责：根据文件扩展名判定预览类型 + 判定是否可预览。
// 分类决定 PreviewViewModel 的加载策略：
//   流式类（IMAGE/VIDEO/AUDIO）—— 直接从下载 URL 边下边播/边下边显，不落盘。
//   缓存类（PDF/TEXT/OFFICE_*）—— 先下载到 app cache 目录再渲染，预览结束删除。
package com.sunyuanling.filesync.previewUtil

/** 预览类型。UNSUPPORTED 表示暂不支持在线预览，界面回退到"下载"。 */
enum class PreviewType {
    IMAGE,
    VIDEO,
    AUDIO,
    PDF,
    TEXT,
    OFFICE_WORD,
    OFFICE_EXCEL,
    OFFICE_PPT,
    UNSUPPORTED;

    /** 该类型是否走"先下载到硬盘再渲染"的缓存策略（否则为流式）。 */
    val needsLocalFile: Boolean
        get() = when (this) {
            PDF, TEXT, OFFICE_WORD, OFFICE_EXCEL, OFFICE_PPT -> true
            else -> false
        }
}

/** 各类型对应的扩展名集合（小写，不含点）。 */
private val IMAGE_EXT = setOf("jpg", "jpeg", "png", "gif", "webp", "bmp", "heic", "heif", "svg")
private val VIDEO_EXT = setOf("mp4", "mkv", "webm", "3gp", "mov", "avi", "flv", "m4v", "ts", "wmv")
private val AUDIO_EXT = setOf("mp3", "aac", "wav", "flac", "ogg", "m4a", "amr", "opus", "wma", "mid")
private val TEXT_EXT = setOf(
    "txt", "log", "md", "markdown", "json", "xml", "yaml", "yml", "csv", "ini", "conf", "properties",
    "kt", "java", "js", "ts", "go", "rs", "py", "c", "cpp", "h", "hpp", "cs", "sh", "bat", "gradle",
    "html", "htm", "css", "sql", "toml"
)
private val WORD_EXT = setOf("doc", "docx")
private val EXCEL_EXT = setOf("xls", "xlsx")
private val PPT_EXT = setOf("ppt", "pptx")

/**
 * 判定预览类型。优先用 [extension]（不含点），为空时从 [name] 末段推断。
 */
fun detectPreviewType(name: String, extension: String = ""): PreviewType {
    // 后端 extension 来自 Go filepath.Ext，带前导点（如 ".mp4"），需去掉再比对；
    // 为空时回退按文件名末段推断。
    val ext = extension.ifBlank { name.substringAfterLast('.', "") }
        .trim()
        .trimStart('.')
        .lowercase()
    if (ext.isEmpty()) return PreviewType.UNSUPPORTED
    return when (ext) {
        in IMAGE_EXT -> PreviewType.IMAGE
        in VIDEO_EXT -> PreviewType.VIDEO
        in AUDIO_EXT -> PreviewType.AUDIO
        in TEXT_EXT -> PreviewType.TEXT
        "pdf" -> PreviewType.PDF
        in WORD_EXT -> PreviewType.OFFICE_WORD
        in EXCEL_EXT -> PreviewType.OFFICE_EXCEL
        in PPT_EXT -> PreviewType.OFFICE_PPT
        else -> PreviewType.UNSUPPORTED
    }
}

// 服务端（internal/thumb）可能有缩略图的扩展名，两边保持一致：
// 图片；视频（内嵌封面，没有就取一帧）；音频（只取内嵌专辑封面，没有封面的服务端会返回 415，界面回退到图标）。
// 刻意不含 ts：它更多是 TypeScript 源文件。
private val THUMBNAIL_IMAGE_EXT = setOf("jpg", "jpeg", "png", "gif", "bmp", "webp", "tif", "tiff")
private val THUMBNAIL_VIDEO_EXT = setOf("mp4", "mkv", "webm", "mov", "avi", "flv", "m4v", "wmv", "3gp", "mpg", "mpeg")
private val THUMBNAIL_AUDIO_EXT = setOf("mp3", "flac", "m4a", "ogg", "opus", "wma")

/** 是否可能有服务端缩略图（只看扩展名）。列表里据此决定先试缩略图还是直接显示类型图标。 */
fun isThumbnailable(name: String): Boolean {
    val ext = name.substringAfterLast('.', "").lowercase()
    return ext in THUMBNAIL_IMAGE_EXT || ext in THUMBNAIL_VIDEO_EXT || ext in THUMBNAIL_AUDIO_EXT
}

/** 是否是 Coil 能直接解码的本地图片：视频/音频的封面只能向服务端要，本地文件喂给 Coil 只会解码失败。 */
fun isLocallyDecodableImage(name: String): Boolean =
    name.substringAfterLast('.', "").lowercase() in THUMBNAIL_IMAGE_EXT

/** 是否可在线预览（非 UNSUPPORTED）。文件列表据此决定点击是"预览"还是"下载"。 */
fun isPreviewable(name: String, extension: String = ""): Boolean =
    detectPreviewType(name, extension) != PreviewType.UNSUPPORTED
