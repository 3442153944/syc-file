// 内存安全的通用规则：软件是长时间挂在托盘里跑的，图表不能陪着无限攒数据。
// 具体节流间隔由调用方按"当前所处状态"决定——简略模式统一按 1 分钟一个点
// （其余通过 WS/轮询收到的帧直接丢弃），不是这个工具函数写死的；这里只提供
// 机制，策略交给每个视图自己决定并传参。
// 保留窗口同理：超过 maxDays 天的旧点从队首丢弃，具体天数由调用方传入
//（简略模式固定 1 天；某个图表如果有自己的天数选择器就传那个选中值）。
// 需要比这个更细的分辨率/更长的时间范围一律走"详情"——针对某个具体实体
//（某个进程/某个端口）单独发请求换一份数据，不在这个常驻 buffer 里攒；
// 切换详情目标或切换维度时上层会重新拉取，不依赖这里的节流状态。
export const OVERVIEW_INTERVAL_MS = 60_000

export interface Timestamped {
  t: number // unix 秒
}

/** 每个调用点各自维护一个节流游标，不要多个数据源共用一个。 */
export function createThrottleCursor(): { value: number } {
  return { value: 0 }
}

/**
 * 尝试追加一个实时点：距上次接受不满 minIntervalMs 就丢弃，否则追加后按 maxDays
 * 裁掉队首。minIntervalMs 缺省是简略模式的 1 分钟，视图所处状态不同可以传别的值。
 * 返回新数组；没接受时原样返回传入的 current（引用不变，方便调用方判断是否真的变了）。
 */
export function throttledAppend<T extends Timestamped>(
  current: T[],
  next: T,
  maxDays: number,
  cursor: { value: number },
  minIntervalMs: number = OVERVIEW_INTERVAL_MS,
): T[] {
  const now = Date.now()
  if (now - cursor.value < minIntervalMs) return current
  cursor.value = now
  const cutoff = Math.floor(now / 1000) - maxDays * 86400
  return [...current, next].filter((p) => p.t >= cutoff)
}
