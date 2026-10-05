// 运行：node --test tests/    （Node 22+，直接跑 TS，不需要额外依赖）
import { test } from 'node:test'
import assert from 'node:assert/strict'
import { merge3, hasConflictMarkers, MARK_MINE, MARK_SEP, MARK_THEIRS } from '../src/utils/merge3.ts'

const lines = (...l: string[]) => l.join('\n') + '\n'

test('双方改的是不同位置：自动合并，没有冲突', () => {
  const base = lines('a', 'b', 'c', 'd', 'e')
  const mine = lines('A', 'b', 'c', 'd', 'e') // 改第 1 行
  const theirs = lines('a', 'b', 'c', 'd', 'E') // 改第 5 行
  const r = merge3(base, mine, theirs)
  assert.equal(r.merged, lines('A', 'b', 'c', 'd', 'E'))
  assert.equal(r.conflicts, 0)
  assert.equal(r.attempted, true)
})

test('同一行被改成不同内容：冲突，两边都保留并带标记', () => {
  const base = lines('a', 'b', 'c')
  const r = merge3(base, lines('a', 'B-mine', 'c'), lines('a', 'B-theirs', 'c'))
  assert.equal(r.conflicts, 1)
  assert.equal(r.merged, lines('a', MARK_MINE, 'B-mine', MARK_SEP, 'B-theirs', MARK_THEIRS, 'c'))
  assert.ok(hasConflictMarkers(r.merged))
})

test('同一行被改成一样的内容：不算冲突', () => {
  const base = lines('a', 'b', 'c')
  const r = merge3(base, lines('a', 'B', 'c'), lines('a', 'B', 'c'))
  assert.deepEqual([r.merged, r.conflicts], [lines('a', 'B', 'c'), 0])
})

test('相邻行的改动按冲突处理（和 git 一致，无法确定先后）', () => {
  const base = lines('a', 'b', 'c', 'd')
  const r = merge3(base, lines('a', 'B', 'c', 'd'), lines('a', 'b', 'C', 'd'))
  assert.equal(r.conflicts, 1)
  assert.ok(r.merged.includes('B') && r.merged.includes('C'))
})

test('中间隔了一行未改动的：不算相邻，能自动合并', () => {
  const base = lines('a', 'b', 'c', 'd')
  const r = merge3(base, lines('A', 'b', 'c', 'd'), lines('a', 'b', 'C', 'd'))
  assert.deepEqual([r.merged, r.conflicts], [lines('A', 'b', 'C', 'd'), 0])
})

test('不同位置各插入一行：都保留', () => {
  const base = lines('a', 'b', 'c', 'd', 'e')
  const r = merge3(base, lines('a', 'mine', 'b', 'c', 'd', 'e'), lines('a', 'b', 'c', 'd', 'theirs', 'e'))
  assert.deepEqual([r.merged, r.conflicts], [lines('a', 'mine', 'b', 'c', 'd', 'theirs', 'e'), 0])
})

test('同一位置插入同样的内容：只留一份；插入不同内容：冲突', () => {
  const base = lines('a', 'b')
  assert.deepEqual(
    [merge3(base, lines('a', 'x', 'b'), lines('a', 'x', 'b')).merged, merge3(base, lines('a', 'x', 'b'), lines('a', 'x', 'b')).conflicts],
    [lines('a', 'x', 'b'), 0],
  )
  assert.equal(merge3(base, lines('a', 'x', 'b'), lines('a', 'y', 'b')).conflicts, 1)
})

test('一方删除、另一方改了别处：都生效', () => {
  const base = lines('a', 'b', 'c', 'd', 'e')
  const r = merge3(base, lines('a', 'b', 'd', 'e'), lines('A', 'b', 'c', 'd', 'e')) // 我删了 c，对方改了 a
  assert.deepEqual([r.merged, r.conflicts], [lines('A', 'b', 'd', 'e'), 0])
})

test('一方删除了对方正在改的那一行：冲突', () => {
  const base = lines('a', 'b', 'c')
  assert.equal(merge3(base, lines('a', 'c'), lines('a', 'B', 'c')).conflicts, 1)
})

test('一方没动 / 双方一致：走快速路径', () => {
  const base = lines('a', 'b')
  assert.equal(merge3(base, base, lines('a', 'B')).merged, lines('a', 'B'))
  assert.equal(merge3(base, lines('A', 'b'), base).merged, lines('A', 'b'))
  assert.equal(merge3(base, lines('X'), lines('X')).merged, lines('X'))
})

test('空 base：两边都是新增内容', () => {
  assert.equal(merge3('', 'mine', 'theirs').conflicts, 1)
  assert.equal(merge3('', 'same', 'same').conflicts, 0)
})

test('没有结尾换行的文件也正确', () => {
  const r = merge3('a\nb\nc', 'A\nb\nc', 'a\nb\nC')
  assert.deepEqual([r.merged, r.conflicts], ['A\nb\nC', 0])
})

test('大文件、改动相距很远：快速合并', () => {
  const base = Array.from({ length: 5000 }, (_, i) => `line ${i}`)
  const mine = [...base]
  const theirs = [...base]
  mine[10] = 'mine edit'
  theirs[4000] = 'theirs edit'
  const t0 = Date.now()
  const r = merge3(base.join('\n'), mine.join('\n'), theirs.join('\n'))
  assert.equal(r.conflicts, 0)
  const expected = [...base]
  expected[10] = 'mine edit'
  expected[4000] = 'theirs edit'
  assert.equal(r.merged, expected.join('\n'))
  assert.ok(Date.now() - t0 < 1000, '5000 行文件合并应在 1 秒内完成')
})

test('两边几乎整份都不一样：放弃合并而不是卡死', () => {
  const base = Array.from({ length: 3000 }, (_, i) => `base ${i}`).join('\n')
  const mine = Array.from({ length: 3000 }, (_, i) => `mine ${i}`).join('\n')
  const theirs = Array.from({ length: 3000 }, (_, i) => `theirs ${i}`).join('\n')
  const t0 = Date.now()
  const r = merge3(base, mine, theirs)
  assert.equal(r.attempted, false)
  assert.equal(r.merged, mine) // 放弃时保留「我的」，不丢我的修改
  assert.ok(Date.now() - t0 < 2000)
})

test('hasConflictMarkers', () => {
  assert.equal(hasConflictMarkers('a\nb'), false)
  assert.equal(hasConflictMarkers(`a\n${MARK_MINE}\nb`), true)
  assert.equal(hasConflictMarkers('a ======= b'), false) // 只认独占一行的标记
})

// ── 模糊测试：位置不重叠的两组随机改动，必须无冲突合并，且结果同时包含双方的改动 ──
function rng(seed: number) {
  return () => {
    seed = (seed * 1664525 + 1013904223) >>> 0
    return seed / 2 ** 32
  }
}

test('随机模糊测试：不重叠的改动永远能无冲突合并', () => {
  const rand = rng(12345)
  for (let iter = 0; iter < 400; iter++) {
    const n = 20 + Math.floor(rand() * 60)
    const base = Array.from({ length: n }, (_, i) => `L${i}`)
    // 我改前半段的若干行，对方改后半段的若干行，中间留出缓冲
    const mid = Math.floor(n / 2)
    const mine = [...base]
    const theirs = [...base]
    const expected = [...base]
    for (let k = 0; k < 1 + Math.floor(rand() * 4); k++) {
      const i = Math.floor(rand() * (mid - 2))
      mine[i] = `mine-${iter}-${k}`
      expected[i] = mine[i]
    }
    for (let k = 0; k < 1 + Math.floor(rand() * 4); k++) {
      const i = mid + 2 + Math.floor(rand() * (n - mid - 2))
      theirs[i] = `theirs-${iter}-${k}`
      expected[i] = theirs[i]
    }
    const r = merge3(base.join('\n'), mine.join('\n'), theirs.join('\n'))
    assert.equal(r.conflicts, 0, `第 ${iter} 轮不该有冲突`)
    assert.equal(r.merged, expected.join('\n'), `第 ${iter} 轮合并结果不对`)
  }
})

test('随机模糊测试：任何输入都不丢任何一方的改动内容（要么合并要么留在冲突里）', () => {
  const rand = rng(777)
  for (let iter = 0; iter < 400; iter++) {
    const n = 5 + Math.floor(rand() * 15)
    const base = Array.from({ length: n }, (_, i) => `L${i}`)
    const edit = (tag: string) => {
      const out = [...base]
      for (let k = 0; k < 1 + Math.floor(rand() * 3); k++) {
        const i = Math.floor(rand() * out.length)
        const kind = rand()
        if (kind < 0.4) out[i] = `${tag}-edit-${k}`
        else if (kind < 0.7) out.splice(i, 0, `${tag}-ins-${k}`)
        else out.splice(i, 1)
      }
      return out
    }
    const mine = edit('M')
    const theirs = edit('T')
    const r = merge3(base.join('\n'), mine.join('\n'), theirs.join('\n'))
    for (const tag of ['M', 'T']) {
      const want = (tag === 'M' ? mine : theirs).filter((l) => l.startsWith(tag + '-'))
      for (const l of want) {
        assert.ok(r.merged.split('\n').includes(l), `第 ${iter} 轮：${tag} 方的改动「${l}」在合并结果里丢了`)
      }
    }
  }
})
