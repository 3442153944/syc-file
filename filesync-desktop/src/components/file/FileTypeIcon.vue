<script setup lang="ts">
import { computed, type Component } from 'vue'
import { NIcon } from 'naive-ui'
import {
  Cube,
  Database,
  File as FileIcon,
  FileAlt,
  FileArchive,
  FileAudio,
  FileCode,
  FileCsv,
  FileExcel,
  FileImage,
  FilePdf,
  FilePowerpoint,
  FileSignature,
  FileVideo,
  FileWord,
  Font,
  Cog,
} from '@vicons/fa'

const props = withDefaults(defineProps<{ name: string; size?: number }>(), { size: 22 })

type IconStyle = { icon: Component; color: string }

const image: IconStyle = { icon: FileImage, color: '#18a058' }
const video: IconStyle = { icon: FileVideo, color: '#7c3aed' }
const audio: IconStyle = { icon: FileAudio, color: '#f0a020' }
const pdf: IconStyle = { icon: FilePdf, color: '#d03050' }
const word: IconStyle = { icon: FileWord, color: '#2f6feb' }
const excel: IconStyle = { icon: FileExcel, color: '#18a058' }
const csv: IconStyle = { icon: FileCsv, color: '#0f766e' }
const powerpoint: IconStyle = { icon: FilePowerpoint, color: '#e8792b' }
const archive: IconStyle = { icon: FileArchive, color: '#c08a2e' }
const code: IconStyle = { icon: FileCode, color: '#2080a0' }
const text: IconStyle = { icon: FileAlt, color: '#6b7280' }
const executable: IconStyle = { icon: Cog, color: '#9b2c2c' }
const signature: IconStyle = { icon: FileSignature, color: '#d03050' }
const font: IconStyle = { icon: Font, color: '#7c3aed' }
const database: IconStyle = { icon: Database, color: '#0f766e' }
const model: IconStyle = { icon: Cube, color: '#8b5cf6' }
const fallback: IconStyle = { icon: FileIcon, color: '#8a8f99' }

const groups: Array<[IconStyle, string[]]> = [
  [image, ['jpg', 'jpeg', 'png', 'gif', 'bmp', 'webp', 'tif', 'tiff', 'svg', 'ico', 'avif', 'heic', 'heif', 'raw']],
  [video, ['mp4', 'mkv', 'webm', 'mov', 'avi', 'flv', 'm4v', 'wmv', '3gp', 'mpg', 'mpeg', 'ogv', 'rmvb', 'vob']],
  [audio, ['mp3', 'flac', 'm4a', 'ogg', 'opus', 'wma', 'wav', 'aac', 'ape', 'mid', 'midi']],
  [pdf, ['pdf']],
  [word, ['doc', 'docx', 'rtf', 'odt', 'pages', 'wps']],
  [excel, ['xls', 'xlsx', 'ods', 'xlsm', 'et']],
  [csv, ['csv', 'tsv']],
  [powerpoint, ['ppt', 'pptx', 'odp', 'key', 'dps']],
  [archive, ['zip', 'rar', '7z', 'tar', 'gz', 'tgz', 'bz2', 'xz', 'zst', 'lz', 'lz4', 'cab', 'iso', 'img']],
  [signature, ['sig', 'asc', 'pgp', 'gpg']],
  [code, [
    'js', 'mjs', 'cjs', 'ts', 'tsx', 'jsx', 'vue', 'go', 'py', 'rs', 'java', 'kt', 'kts', 'c', 'h', 'cpp', 'hpp',
    'cs', 'php', 'rb', 'swift', 'dart', 'lua', 'sh', 'bash', 'zsh', 'fish', 'ps1', 'bat', 'cmd', 'sql', 'html',
    'htm', 'css', 'scss', 'sass', 'less', 'json', 'jsonc', 'yaml', 'yml', 'toml', 'xml', 'gradle',
  ]],
  [text, ['txt', 'md', 'markdown', 'log', 'ini', 'cfg', 'conf', 'config', 'properties', 'env', 'rst', 'tex']],
  [executable, ['exe', 'msi', 'apk', 'aab', 'dmg', 'pkg', 'deb', 'rpm', 'appimage', 'app', 'dll', 'so', 'dylib']],
  [font, ['ttf', 'otf', 'woff', 'woff2', 'eot']],
  [database, ['db', 'sqlite', 'sqlite3', 'mdb', 'accdb', 'dbf']],
  [model, ['dwg', 'dxf', 'stl', 'obj', 'fbx', 'blend', 'gltf', 'glb', '3ds', 'max']],
]

const byExt = new Map<string, IconStyle>()
for (const [style, extensions] of groups) {
  for (const extension of extensions) byExt.set(extension, style)
}

const byName: Record<string, IconStyle> = {
  dockerfile: code,
  makefile: code,
  cmakelists: code,
  readme: text,
  license: text,
  copying: text,
  '.env': text,
  '.gitignore': text,
  '.gitattributes': text,
  '.dockerignore': text,
  '.editorconfig': text,
  '.bashrc': code,
  '.profile': code,
  '.zshrc': code,
  hosts: text,
  crontab: text,
  fstab: text,
}

const style = computed(() => {
  const name = props.name.toLowerCase()
  if (byName[name]) return byName[name]
  const dot = name.lastIndexOf('.')
  if (dot <= 0) return fallback
  return byExt.get(name.slice(dot + 1)) ?? fallback
})
</script>

<template>
  <n-icon :size="size" :color="style.color">
    <component :is="style.icon" />
  </n-icon>
</template>
