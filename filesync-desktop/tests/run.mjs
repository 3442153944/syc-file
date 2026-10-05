// 运行纯逻辑的单元测试（不涉及 Vue / DOM 的工具函数）。
//   node tests/run.mjs
//
// 项目里没有测试框架，系统自带的 Node 也不一定带 TypeScript 支持，所以这里用已有的 typescript 把
// 被测文件和测试文件转成 JS 放到临时目录，再交给 Node 自带的 node:test 运行，不新增任何依赖。
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'unit-'))

// 被测的源文件 + 全部 tests/*.test.ts
const sources = ['src/utils/merge3.ts']
const tests = fs.readdirSync(path.join(root, 'tests')).filter((f) => f.endsWith('.test.ts')).map((f) => 'tests/' + f)

for (const rel of [...sources, ...tests]) {
  const code = fs.readFileSync(path.join(root, rel), 'utf8')
  const out = ts
    .transpileModule(code, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } })
    .outputText.replace(/(from\s+['"][^'"]+)\.ts(['"])/g, '$1.js$2') // import 路径的 .ts → .js
  const dest = path.join(tmp, rel.replace(/\.ts$/, '.js'))
  fs.mkdirSync(path.dirname(dest), { recursive: true })
  fs.writeFileSync(dest, out)
}
fs.writeFileSync(path.join(tmp, 'package.json'), '{"type":"module"}')

const files = tests.map((t) => path.join(tmp, t.replace(/\.ts$/, '.js')))
const r = spawnSync(process.execPath, ['--test', ...files], { stdio: 'inherit' })
fs.rmSync(tmp, { recursive: true, force: true })
process.exit(r.status ?? 1)
