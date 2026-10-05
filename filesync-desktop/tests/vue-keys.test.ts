// 模板规范检查：n-space 里的 <template v-if/v-else> 若有多个根节点，每个根节点必须带 key。
//
// 背景：n-space 会把子节点拍平并按位置复用组件实例，Fragment 上的 key 在拍平时丢失。
// 没有 key 时，v-if/v-else 切换前后同一位置的 n-button 被原地复用；而生产构建的编译器把 @click
// 当作静态属性不再比对，按钮上就残留着上一个分支的点击处理函数——点「保存」实际执行「编辑」，
// 只在打包后出现，dev 下一切正常（见 components/file/TextEditor.vue 里的注释）。
//   node tests/run.mjs
import { test } from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import path from 'node:path'

/** 会拍平子节点的容器 */
const FLATTENING = new Set(['n-space', 'n-button-group', 'n-flex', 'n-breadcrumb'])

interface Node {
  tag: string
  attrs: string
  children: Node[]
  line: number
}

const TAG = /<(\/?)([A-Za-z][\w.-]*)((?:"[^"]*"|'[^']*'|[^'">])*?)(\/?)>/g

/** 把 <template> 部分解析成一棵粗略的标签树（够用于本检查，不是完整的 HTML 解析器）。 */
function parse(src: string): Node {
  const start = src.indexOf('<template>')
  const end = src.lastIndexOf('</template>')
  const body = start >= 0 && end > start ? src.slice(start + '<template>'.length, end) : ''
  const baseLine = src.slice(0, start + '<template>'.length).split('\n').length - 1
  const root: Node = { tag: '#root', attrs: '', children: [], line: 0 }
  const stack: Node[] = [root]
  const clean = body.replace(/<!--[\s\S]*?-->/g, (m) => m.replace(/[^\n]/g, ' ')) // 注释抹成空白，保留换行
  for (const m of clean.matchAll(TAG)) {
    const [, closing, tag, attrs, selfClose] = m
    const line = baseLine + clean.slice(0, m.index).split('\n').length
    if (closing) {
      for (let i = stack.length - 1; i > 0; i--) {
        if (stack[i].tag === tag) {
          stack.length = i
          break
        }
      }
      continue
    }
    const node: Node = { tag, attrs, children: [], line }
    stack[stack.length - 1].children.push(node)
    if (!selfClose && !['br', 'img', 'input', 'hr'].includes(tag)) stack.push(node)
  }
  return root
}

const hasKey = (n: Node) => /(^|\s):?key\s*=/.test(n.attrs)
const isCondTemplate = (n: Node) =>
  n.tag === 'template' && /\sv-(if|else-if|else)\b/.test(' ' + n.attrs) && !/\s#|\sv-slot/.test(' ' + n.attrs)

/** 返回所有违规位置：n-space 下的条件 <template> 有 ≥2 个根节点，其中有无 key 的组件 */
export function findViolations(src: string): string[] {
  const out: string[] = []
  const walk = (n: Node) => {
    if (FLATTENING.has(n.tag)) {
      for (const t of n.children.filter(isCondTemplate)) {
        if (t.children.length < 2) continue // 单根节点编译成带 key 的元素，不会被拍平丢 key
        for (const c of t.children) {
          const isComponent = /^(n-|[A-Z])/.test(c.tag)
          if (isComponent && !hasKey(c)) out.push(`第 ${c.line} 行 <${c.tag}> 缺少 key（位于 ${n.tag} 的条件 <template> 中，第 ${t.line} 行）`)
        }
      }
    }
    n.children.forEach(walk)
  }
  walk(parse(src))
  return out
}

test('检查器自身：能识别出缺 key 的写法，放过带 key 的写法', () => {
  const bad = `<template>
  <n-space>
    <template v-if="!editing">
      <n-button @click="a">编辑</n-button>
      <n-button @click="b">重载</n-button>
    </template>
    <template v-else>
      <n-button @click="c">保存</n-button>
      <n-button @click="d">放弃</n-button>
    </template>
  </n-space>
</template>`
  assert.equal(findViolations(bad).length, 4)

  const good = bad.replace(/<n-button /g, (_m) => `<n-button key="k${Math.random()}" `)
  assert.equal(findViolations(good).length, 0)

  // 单根节点的条件 template、非 n-space 容器：不在检查范围
  assert.equal(
    findViolations(`<template><n-space><template v-if="x"><n-button @click="a">a</n-button></template></n-space></template>`).length,
    0,
  )
  assert.equal(
    findViolations(`<template><div><template v-if="x"><n-button>a</n-button><n-button>b</n-button></template></div></template>`).length,
    0,
  )
  // 属性值里带 > 不能把标签解析坏
  assert.equal(
    findViolations(`<template><n-space><template v-if="a > 1"><n-button v-if="b > 2" @click="() => x()">a</n-button><n-button>b</n-button></template></n-space></template>`).length,
    2,
  )
})

test('src 下所有 .vue：n-space 里的多根条件 <template> 里的组件都带 key', () => {
  const srcDir = path.resolve(process.cwd(), 'src')
  assert.ok(fs.existsSync(srcDir), `找不到 ${srcDir}，请在 filesync-desktop 目录下运行`)
  const files: string[] = []
  const walkDir = (d: string) => {
    for (const e of fs.readdirSync(d, { withFileTypes: true })) {
      const p = path.join(d, e.name)
      if (e.isDirectory()) walkDir(p)
      else if (e.name.endsWith('.vue')) files.push(p)
    }
  }
  walkDir(srcDir)
  const problems = files.flatMap((f) => findViolations(fs.readFileSync(f, 'utf8')).map((v) => `${path.relative(process.cwd(), f)}: ${v}`))
  assert.deepEqual(problems, [], '\n' + problems.join('\n'))
})
