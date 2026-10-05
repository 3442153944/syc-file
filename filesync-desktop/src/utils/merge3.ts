// 文本三方合并（按行）。
//
// 场景：我基于版本 base 做了修改得到 mine，保存时发现别人已经把文件改成了 theirs。
// 两边改的是不同位置就自动合并；改到了同一处才算冲突，冲突处用 git 风格的标记留给人决定。
//
// 只用「可擦除」的 TS 语法（没有 enum/namespace），这样 Node 能直接跑测试，不依赖打包工具。

export interface MergeResult {
  /** 合并后的文本。有冲突时，冲突处带 <<<<<<< / ======= / >>>>>>> 标记 */
  merged: string
  /** 冲突块数；0 表示完全自动合并成功 */
  conflicts: number
  /** 为 false 表示文件太大或差异太分散，算法放弃了（此时 merged 是整份「我的」版本，conflicts 记为 1） */
  attempted: boolean
}

/** base 中 [start, end) 这段被替换成 lines。start == end 表示纯插入，lines 为空表示纯删除。 */
interface Hunk {
  start: number
  end: number
  lines: string[]
}

// LCS 动态规划的格子数上限（约 16MB 的 Uint32Array）。去掉首尾相同的行之后，真正参与比较的只是改动区域，
// 正常编辑远远用不到这么多；超过说明两个版本几乎整份不同，没有合并的意义。
const MAX_CELLS = 4_000_000

export const MARK_MINE = '<<<<<<< 我的修改'
export const MARK_SEP = '======='
export const MARK_THEIRS = '>>>>>>> 对方的修改'

/** 求把 a 变成 b 的最小替换块列表；太大则返回 null。 */
function diffHunks(a: string[], b: string[]): Hunk[] | null {
  // 先去掉首尾相同的行：编辑通常只动一小块，这样 DP 只需在改动区域上做
  let pre = 0
  while (pre < a.length && pre < b.length && a[pre] === b[pre]) pre++
  let suf = 0
  while (suf < a.length - pre && suf < b.length - pre && a[a.length - 1 - suf] === b[b.length - 1 - suf]) suf++
  const aMid = a.slice(pre, a.length - suf)
  const bMid = b.slice(pre, b.length - suf)
  if (aMid.length === 0 && bMid.length === 0) return []
  if (aMid.length === 0) return [{ start: pre, end: pre, lines: bMid }]
  if (bMid.length === 0) return [{ start: pre, end: pre + aMid.length, lines: [] }]

  const n = aMid.length
  const m = bMid.length
  if ((n + 1) * (m + 1) > MAX_CELLS) return null

  // dp[i][j] = aMid[i..] 与 bMid[j..] 的最长公共子序列长度
  const w = m + 1
  const dp = new Uint32Array((n + 1) * w)
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i * w + j] =
        aMid[i] === bMid[j] ? dp[(i + 1) * w + j + 1] + 1 : Math.max(dp[(i + 1) * w + j], dp[i * w + j + 1])
    }
  }

  // 沿 DP 表走出匹配的行对，匹配对之间的空隙就是替换块
  const hunks: Hunk[] = []
  let i = 0
  let j = 0
  let hs = -1 // 当前块在 aMid 中的起点
  let hj = 0 // 当前块在 bMid 中的起点
  const flush = (aEnd: number, bEnd: number) => {
    if (hs >= 0) {
      hunks.push({ start: pre + hs, end: pre + aEnd, lines: bMid.slice(hj, bEnd) })
      hs = -1
    }
  }
  while (i < n || j < m) {
    if (i < n && j < m && aMid[i] === bMid[j]) {
      flush(i, j)
      i++
      j++
    } else {
      if (hs < 0) {
        hs = i
        hj = j
      }
      if (j < m && (i >= n || dp[i * w + j + 1] >= dp[(i + 1) * w + j])) j++
      else i++
    }
  }
  flush(n, m)
  return hunks
}

/** 把落在 base[start, end) 内的一组替换块应用上去，得到这一段的新内容。 */
function applyWithin(base: string[], start: number, end: number, hunks: Hunk[]): string[] {
  const out: string[] = []
  let pos = start
  for (const h of hunks) {
    out.push(...base.slice(pos, h.start), ...h.lines)
    pos = h.end
  }
  out.push(...base.slice(pos, end))
  return out
}

const sameLines = (a: string[], b: string[]) => a.length === b.length && a.every((v, i) => v === b[i])

export function merge3(base: string, mine: string, theirs: string): MergeResult {
  // 快速路径：一方没动，或双方改成了一样的内容
  if (mine === theirs) return { merged: mine, conflicts: 0, attempted: true }
  if (base === mine) return { merged: theirs, conflicts: 0, attempted: true }
  if (base === theirs) return { merged: mine, conflicts: 0, attempted: true }

  const b = base.split('\n')
  const hm = diffHunks(b, mine.split('\n'))
  const ht = diffHunks(b, theirs.split('\n'))
  if (hm === null || ht === null) return { merged: mine, conflicts: 1, attempted: false }

  const out: string[] = []
  let pos = 0
  let conflicts = 0
  let i = 0
  let j = 0
  while (i < hm.length || j < ht.length) {
    const a = hm[i]
    const t = ht[j]
    // 其中一方的下一块完全在另一方之前（中间至少隔一行未改动的）：直接应用，互不影响
    if (a && (!t || a.end < t.start)) {
      out.push(...b.slice(pos, a.start), ...a.lines)
      pos = a.end
      i++
      continue
    }
    if (t && (!a || t.end < a.start)) {
      out.push(...b.slice(pos, t.start), ...t.lines)
      pos = t.end
      j++
      continue
    }

    // 走到这里两边都还有块（上面两个分支已处理了「只剩一边」的情况）；这个判断只是让类型检查通过
    if (!a || !t) break

    // 两边的块重叠或相邻（相邻也算：和 git 一致，相邻行的改动无法确定谁先谁后）：
    // 把所有连环重叠的块收成一簇，簇内作为一个整体处理
    const start = Math.min(a.start, t.start)
    let end = Math.max(a.end, t.end)
    const cm: Hunk[] = []
    const ct: Hunk[] = []
    for (;;) {
      let grew = false
      while (i < hm.length && hm[i].start <= end) {
        cm.push(hm[i])
        end = Math.max(end, hm[i].end)
        i++
        grew = true
      }
      while (j < ht.length && ht[j].start <= end) {
        ct.push(ht[j])
        end = Math.max(end, ht[j].end)
        j++
        grew = true
      }
      if (!grew) break
    }

    const mineSide = applyWithin(b, start, end, cm)
    const theirSide = applyWithin(b, start, end, ct)
    out.push(...b.slice(pos, start))
    if (sameLines(mineSide, theirSide)) {
      // 两边把这一处改成了同样的结果：不算冲突
      out.push(...mineSide)
    } else {
      conflicts++
      out.push(MARK_MINE, ...mineSide, MARK_SEP, ...theirSide, MARK_THEIRS)
    }
    pos = end
  }
  out.push(...b.slice(pos))
  return { merged: out.join('\n'), conflicts, attempted: true }
}

/** 文本里是否还留有未解决的冲突标记（保存前提醒用）。 */
export function hasConflictMarkers(text: string): boolean {
  return text.split('\n').some((l) => l === MARK_MINE || l === MARK_SEP || l === MARK_THEIRS)
}
