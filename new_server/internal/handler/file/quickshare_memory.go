package file

import "sync"

// quickShareMemStore 粘贴快传里小文件的"内存模拟磁盘"：share_code -> 原始字节。
// 单进程本地存储，够用（个人使用场景，单实例部署，没有跨实例共享的需要）。
// 容量由 destroyShareLink 的正常销毁流程 + 配额上限（quick_share.max_capacity_bytes）
// 双重兜底，不需要额外的大小驱逐策略；进程重启会清空，配套的 expireStaleMemoryShares
// 负责把因此失效的 DB 行也一并标记销毁，避免配额被"内容已经不在了"的记录占着。
var quickShareMemStore sync.Map
