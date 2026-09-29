// 必须是第一个 import：桌面端把 console.* 与未捕获异常汇入 Rust 统一日志
// （日志窗口 + log/filesync.log）。装得越早，启动阶段的日志丢得越少。
import "./utils/consoleBridge.setup";
import {createApp} from "vue";
import App from "./App.vue";
import {pinia} from "./store/useStore.ts";
import {router} from "./router/useRouter.ts"
import naive from 'naive-ui'
import {initNet} from "./api/net"
import {initDeviceId} from "./api/platform"

const app = createApp(App);
app.use(naive)
app.use(pinia)
app.use(router)

// 网络节点表要在第一条请求之前就位：Tauri 下从 Rust 拉当前节点并订阅切换事件，
// Web 下从 localStorage 恢复。不 await —— 拉不到也有兜底地址，不能卡住首屏。
initNet().catch(e => console.warn('[net] 初始化失败，使用兜底地址', e))
// 粘贴快传等纯前端 fetch/XHR 请求要同步取 device id，提前缓存好
initDeviceId().catch(e => console.warn('[device] 初始化失败，Device-Id 将为空', e))

app.mount("#app");
