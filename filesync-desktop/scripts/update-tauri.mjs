import path from 'node:path'
import {fileURLToPath} from 'node:url'
import {spawnSync} from 'node:child_process'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const pnpm = path.join(root, 'node_modules', 'pnpm', 'bin', 'pnpm.cjs')
const steps = [
  [process.execPath, [pnpm, 'update', '@tauri-apps/api', '@tauri-apps/plugin-dialog', '@tauri-apps/plugin-opener', '@tauri-apps/cli']],
  ['cargo', ['update', '--manifest-path', 'src-tauri/Cargo.toml', '-p', 'tauri', '-p', 'tauri-plugin-dialog', '-p', 'tauri-plugin-opener', '-p', 'tauri-plugin-global-shortcut', '-p', 'keyring']],
]
for (const [command, args] of steps) {
  const result = spawnSync(command, args, {cwd: root, stdio: 'inherit'})
  if (result.error) throw result.error
  if (result.status !== 0) process.exit(result.status ?? 1)
}
