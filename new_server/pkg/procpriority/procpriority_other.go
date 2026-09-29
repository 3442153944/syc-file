//go:build !windows

// 非 Windows 平台的空实现：优先级提升是 Windows 特有的调度器概念，Linux/macOS
// 下等价操作是 nice/renice，这个项目目前只在 Windows 工作站上部署，没必要跟着
// 实现一份，先占个位置保证跨平台能编译。
package procpriority

func Raise() {}
