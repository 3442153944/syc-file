/**
 * consoleBridge.ts — 桌面端把前端的 console.* 与未捕获异常汇入 Rust 统一日志。
 *
 * 效果：日志窗口和 log/filesync.log 里是前后端合在一起的完整时间线，不用再开 DevTools
 * （打包后的客户端里 DevTools 本来就打不开，前端报错以前等于直接蒸发）。
 *
 * 原始 console 行为保留：先照常输出到 DevTools，再转发一份。
 *
 * 几条硬约束：
 *
 *  1. **不能形成回环**。转发靠 invoke，invoke 失败时若再走被接管的 console.error，
 *     就会无限递归。所以内部出错一律用保存下来的原始 console 方法输出，并用
 *     `forwarding` 标记挡住转发过程中产生的任何 console 调用。
 *
 *  2. **日志窗口本身不接管**。日志窗口渲染每一行时若产生 Vue 警告，警告被转发、
 *     又推回日志窗口渲染、再产生警告……这是一个真实的反馈环。它的 console 留在 DevTools。
 *
 *  3. **批量发送、限速**。每条单独 IPC 太重，所以攒批发送。前端刷屏（比如渲染循环里
 *     每帧一条警告）时也不能把 IPC、日志文件、日志窗口一起拖垮 —— 每秒超过
 *     RATE_LIMIT 条的直接丢弃，并补一条"丢弃了 N 条"让人知道发生过刷屏。
 *     注意限的是速率而不是队列长度：flush 是同步的，队列攒到 BATCH_SIZE 就发走，
 *     永远涨不到任何"队列上限"，那种上限挡不住刷屏。
 *
 *  4. **脱敏**。下载直链的 query 里带着 token，报错信息里经常原样带上 URL；
 *     日志文件是明文落盘的，写进去之前把 token 抹掉。
 */
import {invoke, isTauri} from '@tauri-apps/api/core'
import {getCurrentWindow} from '@tauri-apps/api/window'

type Level = 'debug' | 'info' | 'warn' | 'error'

interface Entry {
    ts: number
    level: Level
    source: string
    message: string
}

/** 攒够这么多条立即发送 */
const BATCH_SIZE = 50
/** 否则最多等这么久发送一批 */
const FLUSH_DELAY_MS = 200
/** 每秒最多转发多少条，超出丢弃 */
const RATE_LIMIT = 200
/** 单条消息长度上限：大对象序列化出来可能几 MB */
const MAX_MESSAGE_CHARS = 8000
const MAX_ARG_CHARS = 2000

let installed = false

export function installConsoleBridge(): void {
    if (installed || !isTauri()) return

    let label = 'main'
    try {
        label = getCurrentWindow().label
    } catch { /* 取不到按主窗口处理 */
    }
    if (label === 'logs') return // 见顶部约束 2
    installed = true

    const source = label === 'main' ? 'web' : `web/${label}`

    // 保存原始方法：内部出错只能用它们输出，否则会回环（约束 1）
    const original = {
        debug: console.debug.bind(console),
        log: console.log.bind(console),
        info: console.info.bind(console),
        warn: console.warn.bind(console),
        error: console.error.bind(console),
    }

    const queue: Entry[] = []
    let dropped = 0
    let timer: ReturnType<typeof setTimeout> | null = null
    let forwarding = false
    // 限速：按自然秒计数
    let windowStart = 0
    let windowCount = 0

    const enqueue = (level: Level, message: string) => {
        if (forwarding) return
        const now = Date.now()
        if (now - windowStart >= 1000) {
            windowStart = now
            windowCount = 0
        }
        if (windowCount >= RATE_LIMIT) {
            dropped++
            // 刷屏之后可能再也没有新日志，得保证"丢弃了 N 条"最终能发出去
            if (!timer) timer = setTimeout(flush, FLUSH_DELAY_MS)
            return
        }
        windowCount++
        queue.push({ts: now, level, source, message})
        if (queue.length >= BATCH_SIZE) {
            flush()
        } else if (!timer) {
            timer = setTimeout(flush, FLUSH_DELAY_MS)
        }
    }

    const flush = () => {
        if (timer) {
            clearTimeout(timer)
            timer = null
        }
        if (queue.length === 0 && dropped === 0) return
        const batch = queue.splice(0, queue.length)
        if (dropped > 0) {
            batch.push({
                ts: Date.now(),
                level: 'warn',
                source,
                message: `前端日志过多，已丢弃 ${dropped} 条`,
            })
            dropped = 0
        }
        forwarding = true
        try {
            invoke('log_from_frontend', {entries: batch}).catch((e) => {
                // 转发失败只能记在 DevTools 里：再走 console.error 就回环了
                original.error('[consoleBridge] 日志转发失败', e)
            })
        } catch (e) {
            original.error('[consoleBridge] 日志转发失败', e)
        } finally {
            forwarding = false
        }
    }

    const hook = (method: keyof typeof original, level: Level) => {
        console[method] = (...args: unknown[]) => {
            original[method](...args)
            try {
                enqueue(level, redact(formatArgs(args)))
            } catch (e) {
                original.error('[consoleBridge] 格式化日志失败', e)
            }
        }
    }

    hook('debug', 'debug')
    hook('log', 'info')
    hook('info', 'info')
    hook('warn', 'warn')
    hook('error', 'error')

    // 未捕获异常 / 未处理的 Promise 拒绝：浏览器自己会打印到 DevTools，这里只转发
    window.addEventListener('error', (e) => {
        const detail = e.error ? formatValue(e.error) : `${e.message} (${e.filename}:${e.lineno}:${e.colno})`
        enqueue('error', redact(`未捕获异常: ${detail}`))
    })
    window.addEventListener('unhandledrejection', (e) => {
        enqueue('error', redact(`未处理的 Promise 拒绝: ${formatValue(e.reason)}`))
    })

    // 窗口关闭/隐藏前把队列里剩下的发出去
    window.addEventListener('pagehide', flush)
}

// ── 格式化 ───────────────────────────────────────────────────────────────────

/**
 * 按 console 的规则拼接参数：首个参数是字符串时支持 %s %d %i %f %o %O %c 占位符
 * （%c 是 CSS 样式，吃掉对应参数但不输出——Vue DevTools 之类的库大量使用它）。
 */
function formatArgs(args: unknown[]): string {
    if (args.length === 0) return ''
    const rest = [...args]
    let head = ''

    if (typeof rest[0] === 'string' && rest[0].includes('%')) {
        const fmt = rest.shift() as string
        head = fmt.replace(/%([sdifoOc%])/g, (m, spec: string) => {
            if (spec === '%') return '%'
            if (rest.length === 0) return m
            const v = rest.shift()
            switch (spec) {
                case 'c':
                    return ''
                case 'd':
                case 'i':
                    return String(parseInt(String(v), 10))
                case 'f':
                    return String(parseFloat(String(v)))
                case 's':
                    return typeof v === 'string' ? v : formatValue(v)
                default:
                    return formatValue(v)
            }
        })
    }

    const parts = rest.map((a) => (typeof a === 'string' ? a : formatValue(a)))
    const message = [head, ...parts].filter((p) => p !== '').join(' ')
    return message.length > MAX_MESSAGE_CHARS ? message.slice(0, MAX_MESSAGE_CHARS) + '…（已截断）' : message
}

function formatValue(v: unknown): string {
    if (v instanceof Error) {
        // stack 通常已包含 "Name: message" 这一行，没有时再手动拼
        const s = v.stack && v.stack.includes(v.message) ? v.stack : `${v.name}: ${v.message}${v.stack ? '\n' + v.stack : ''}`
        return truncate(s)
    }
    if (v === undefined) return 'undefined'
    if (typeof v === 'function') return `[Function ${v.name || 'anonymous'}]`
    if (typeof v !== 'object' || v === null) return String(v)
    return truncate(safeStringify(v))
}

/** JSON.stringify，但能处理循环引用、DOM 节点和 BigInt，任何失败都有兜底 */
function safeStringify(v: object): string {
    const seen = new WeakSet<object>()
    try {
        return JSON.stringify(v, (_k, val) => {
            if (typeof val === 'bigint') return `${val}n`
            if (typeof Node !== 'undefined' && val instanceof Node) return `[${val.nodeName}]`
            if (val instanceof Error) return formatValue(val)
            if (val && typeof val === 'object') {
                // 同一对象第二次出现（循环引用或单纯被引用两次）不再展开
                if (seen.has(val)) return '[重复引用]'
                seen.add(val)
            }
            return val
        })
    } catch {
        return Object.prototype.toString.call(v)
    }
}

function truncate(s: string): string {
    return s.length > MAX_ARG_CHARS ? s.slice(0, MAX_ARG_CHARS) + '…' : s
}

/**
 * 抹掉 token：URL query 里的 token=xxx、JSON 里的 "token": "xxx"、请求头 Token: xxx。
 * 日志文件明文落盘，不能留下可直接冒用的登录凭证。
 */
export function redact(s: string): string {
    return s
        .replace(/([?&]token=)[^&\s"'<>]+/gi, '$1***')
        .replace(/("(?:token|new_token|newToken)"\s*:\s*")[^"]+(")/gi, '$1***$2')
        .replace(/(\bToken:\s*)[A-Za-z0-9._\-]+/g, '$1***')
}
