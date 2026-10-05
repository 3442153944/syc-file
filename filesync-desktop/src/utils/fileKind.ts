// 按扩展名判断文件能不能预览、有没有缩略图。
//
// 两个集合含义不同，不能混用：
//  - 缩略图（hasThumbnail）：服务端 internal/thumb 能生成的类型，两边必须一致。
//    图片；视频（文件自带封面，没有就取一帧）；音频（只取内嵌专辑封面，没有封面服务端返回 415，界面回退到图标）。
//    刻意不含 ts：它更多是 TypeScript 源文件。
//  - 预览（previewKind）：webview 自己能播放/显示的类型，这是浏览器引擎的能力，不是服务端的。
//    例如 tif 能出缩略图，但 <img> 显示不了；mkv 在多数 webview 里播不了，这类走「下载」。

export type PreviewKind = 'image' | 'video' | 'audio' | 'text' | 'none'

const ext = (name: string) => name.slice(name.lastIndexOf('.') + 1).toLowerCase()
const hasExt = (name: string) => name.lastIndexOf('.') > 0

const THUMB_IMAGE = new Set(['jpg', 'jpeg', 'png', 'gif', 'bmp', 'webp', 'tif', 'tiff'])
const THUMB_VIDEO = new Set(['mp4', 'mkv', 'webm', 'mov', 'avi', 'flv', 'm4v', 'wmv', '3gp', 'mpg', 'mpeg'])
const THUMB_AUDIO = new Set(['mp3', 'flac', 'm4a', 'ogg', 'opus', 'wma'])

const PREVIEW_IMAGE = new Set(['jpg', 'jpeg', 'png', 'gif', 'webp', 'bmp', 'svg', 'avif', 'ico'])
const PREVIEW_VIDEO = new Set(['mp4', 'webm', 'mov', 'm4v', 'ogv'])
const PREVIEW_AUDIO = new Set(['mp3', 'wav', 'flac', 'm4a', 'aac', 'ogg', 'opus'])

// 可在线查看/编辑的文本：与服务端 internal/handler/file/text.go 的 editableExts / editableNames 保持一致。
// 服务端还会按内容再把关（二进制、非 UTF-8、超过 2MB 会被拒绝，界面会给出原因），这里只按文件名预判。
const TEXT_EXT = new Set([
  'txt', 'md', 'markdown', 'log', 'csv', 'tsv', 'json', 'jsonc', 'yaml', 'yml', 'toml', 'ini', 'cfg', 'conf', 'config',
  'properties', 'env', 'xml', 'html', 'htm', 'css', 'scss', 'js', 'mjs', 'ts', 'vue', 'go', 'py', 'rs', 'java', 'kt',
  'c', 'h', 'cpp', 'hpp', 'cs', 'sh', 'bash', 'zsh', 'bat', 'cmd', 'ps1', 'sql', 'gradle', 'lock', 'gitignore',
  'editorconfig', 'service', 'rules',
])
const TEXT_NAMES = new Set([
  'dockerfile', 'makefile', 'readme', 'license', 'hosts', 'crontab', '.env', '.gitignore', '.gitattributes',
  '.dockerignore', '.editorconfig', '.bashrc', '.profile', '.zshrc', 'fstab',
])

/** 服务端可能有缩略图（只看扩展名；音频没有内嵌封面时服务端会 415，调用方要能回退）。 */
export function hasThumbnail(name: string): boolean {
  if (!hasExt(name)) return false
  const e = ext(name)
  return THUMB_IMAGE.has(e) || THUMB_VIDEO.has(e) || THUMB_AUDIO.has(e)
}

/** 应用内预览的类型；none 表示不能预览（走下载）。 */
export function previewKind(name: string): PreviewKind {
  if (TEXT_NAMES.has(name.toLowerCase())) return 'text'
  if (!hasExt(name)) return 'none'
  const e = ext(name)
  if (PREVIEW_IMAGE.has(e)) return 'image'
  if (PREVIEW_VIDEO.has(e)) return 'video'
  if (PREVIEW_AUDIO.has(e)) return 'audio'
  if (TEXT_EXT.has(e)) return 'text'
  return 'none'
}
