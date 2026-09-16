// 副作用模块：被 main.ts 作为**第一个** import 引入。
//
// 为什么不直接在 main.ts 里调用 installConsoleBridge()：ES 模块的 import 会被提升，
// 写在 import 语句之间的调用实际要等所有 import（Vue、App.vue、各页面模块…）求值完
// 才执行，这期间的日志就漏了。放进第一个被求值的模块里，才是真正最先接管。
import {installConsoleBridge} from './consoleBridge'

installConsoleBridge()
