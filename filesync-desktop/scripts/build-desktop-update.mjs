// 构建安装包与配套 .sig；私钥从构建机读取，不入库。
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import {fileURLToPath} from 'node:url'
import {spawnSync} from 'node:child_process'
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const args = process.argv.slice(2)
const flag = args.indexOf('--version')
let version
if (flag >= 0) {
  version = args[flag + 1]
  if (!/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(version || '')) throw new Error('--version 应为稳定版本 x.y.z')
  const parts = version.split(".").map(Number)
  if (parts[1] > 99 || parts[2] > 99 || !Number.isSafeInteger(parts[0] * 10000 + parts[1] * 100 + parts[2])) throw new Error("版本号超出发布支持的范围")
  args.splice(flag, 2)
}
const env = {...process.env}
const keyFile = env.FILESYNC_SIGNING_KEY_PATH || path.join(os.homedir(), '.filesync-signing', 'desktop.key')
if (!env.TAURI_SIGNING_PRIVATE_KEY) {
  if (!fs.existsSync(keyFile)) throw new Error('未找到签名私钥；设置 FILESYNC_SIGNING_KEY_PATH 或 TAURI_SIGNING_PRIVATE_KEY')
  env.TAURI_SIGNING_PRIVATE_KEY = fs.readFileSync(keyFile, 'utf8').trim()
}
env.TAURI_SIGNING_PRIVATE_KEY_PASSWORD ??= ''
const overlay = {bundle:{createUpdaterArtifacts:true}, ...(version ? {version} : {})}
const bundles = process.platform === 'win32' ? 'nsis' : process.platform === 'linux' ? 'deb,appimage' : 'app'
const cli = path.join(root, 'node_modules', '@tauri-apps', 'cli', 'tauri.js')
const buildArgs = [cli, 'build', '--config', JSON.stringify(overlay)]
if (!args.includes('--bundles')) buildArgs.push('--bundles', bundles)
buildArgs.push(...args)
console.info(`构建桌面更新包${version ? ' ' + version : ''}（安装包及配套签名）`)
const result = spawnSync(process.execPath, buildArgs, {cwd:root, env, stdio:'inherit'})
if (result.error) throw result.error
process.exit(result.status ?? 1)
