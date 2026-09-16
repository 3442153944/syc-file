/**
 * speedMeter.ts — 传输速度测量 + 字节/速度/剩余时间的格式化。
 *
 * 为什么用滑动窗口而不是「总字节 / 总耗时」：平均速度对"现在快不快"毫无参考价值，
 * 网络一抖、隧道一换，平均值要很久才反映出来。这里只看最近 WINDOW_MS 内的进度。
 *
 * 为什么分母用「现在」而不是「最后一个采样点」：进度回调粒度不均匀（普通上传每完成
 * 一个 4MiB 分片才报一次），链路卡住时不会有新采样。若分母用最后采样时刻，速度会
 * 冻结在卡住前的值；用现在，速度会随等待时间自然衰减，卡住就能看出来。
 * 这也意味着调用方要周期性调 rate()（store 里有个 1 秒的 ticker）。
 */

const WINDOW_MS = 5000

/** 采样跨度小于它不出速度：两三百毫秒内的差值噪声太大 */
const MIN_SPAN_MS = 500

interface Sample {
  t: number
  bytes: number
}

export class SpeedMeter {
  private samples: Sample[] = []
  /** 真实传输的起点，供完成后算平均速度（不随窗口裁剪） */
  private origin: Sample | null = null
  private pushes = 0

  /**
   * @param leadingReports 开头有几次上报只是簿记、不代表真实传输。
   *
   * 分片上传（Rust chunked_uploader 与 Web chunkedUploader 行为一致）会先报两次：
   * 算完哈希报 0，init 返回后报「服务端已有的字节数」—— 断点续传时这是几百 MB，
   * 秒传时直接是满的。这两次之间只隔了一次 init 往返，要是算进速度，会凭空冒出
   * 几百 MB/s 的尖峰。所以分片上传传 2：这两次只用来确定基线，从第三次开始计速。
   *
   * 快传走 XHR，每次 upload.onprogress 都是真实发出的字节，传 0。
   *
   * （曾经试过按「两次上报间隔很短」来判断簿记，结果 XHR 每 50ms 一次的正常进度
   * 全被当成簿记吞掉，快传速度恒为 0；而 Tauri 续传时 init 要走网络，间隔反而不短，
   * 尖峰照样漏过。判据必须来自上传器的实际上报协议，不能靠时间猜。）
   */
  private readonly leadingReports: number

  constructor(leadingReports = 0) {
    this.leadingReports = leadingReports
  }

  /** 记录一次「累计已传字节」。 */
  push(bytes: number, t = Date.now()): void {
    this.pushes++
    const last = this.samples[this.samples.length - 1]

    // 字节数倒退 = 上传重新开始了（会话过期后重新 init）。重新 init 之后只会再报
    // 一次「已有字节数」，这一次本身就是新的基线，不需要再跳过前导上报。
    if (last && bytes < last.bytes) {
      this.samples = [{t, bytes}]
      this.origin = {t, bytes}
      return
    }

    // 前导簿记上报：只更新基线，不形成速度
    if (this.pushes <= this.leadingReports) {
      this.samples = [{t, bytes}]
      this.origin = {t, bytes}
      return
    }

    this.samples.push({t, bytes})
    if (!this.origin) this.origin = {t, bytes}
    this.trim(t)
  }

  /** 当前速度（字节/秒）。没有足够采样时返回 0。 */
  rate(now = Date.now()): number {
    this.trim(now)
    if (this.samples.length === 0) return 0
    const first = this.samples[0]
    const last = this.samples[this.samples.length - 1]
    const span = now - first.t
    if (span < MIN_SPAN_MS) return 0
    return Math.max(0, ((last.bytes - first.bytes) * 1000) / span)
  }

  /**
   * 整段平均速度（字节/秒），用于已完成的任务。
   * 耗时不足 1 秒的返回 0：秒传或极小文件，算出来的数字没有意义。
   */
  average(finishedAt = Date.now()): number {
    const last = this.samples[this.samples.length - 1]
    if (!this.origin || !last) return 0
    const span = finishedAt - this.origin.t
    if (span < 1000) return 0
    return Math.max(0, ((last.bytes - this.origin.bytes) * 1000) / span)
  }

  /**
   * 是否已进入真实传输阶段（前导簿记上报都收到了）。
   * 在此之前界面应显示「准备中」而不是 0 速 —— 分片上传此时还在本地算哈希、等 init。
   */
  get started(): boolean {
    return this.pushes >= Math.max(1, this.leadingReports)
  }

  /**
   * 只保留窗口内的采样，但始终留一个窗口起点之前的采样当基线 ——
   * 否则进度稀疏时窗口里可能只剩一个点，永远算不出速度。
   */
  private trim(now: number): void {
    const cutoff = now - WINDOW_MS
    while (this.samples.length >= 2 && this.samples[1].t <= cutoff) {
      this.samples.shift()
    }
  }
}

// ── 格式化 ───────────────────────────────────────────────────────────────────

export function formatBytes(n: number): string {
  if (!n || n < 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let i = 0
  let v = n
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(i === 0 ? 0 : v < 10 ? 2 : 1)} ${units[i]}`
}

export function formatSpeed(bytesPerSec: number): string {
  if (!bytesPerSec || bytesPerSec <= 0) return '—'
  return `${formatBytes(bytesPerSec)}/s`
}

/** 剩余时间。速度为 0 或剩余量未知时返回空串，调用方据此不显示。 */
export function formatEta(remainingBytes: number, bytesPerSec: number): string {
  if (!bytesPerSec || bytesPerSec <= 0 || remainingBytes <= 0) return ''
  const sec = Math.ceil(remainingBytes / bytesPerSec)
  if (sec < 60) return `${sec} 秒`
  if (sec < 3600) return `${Math.floor(sec / 60)} 分 ${sec % 60} 秒`
  const h = Math.floor(sec / 3600)
  return h >= 100 ? '很久' : `${h} 小时 ${Math.floor((sec % 3600) / 60)} 分`
}
