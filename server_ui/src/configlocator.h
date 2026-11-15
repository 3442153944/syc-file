#pragma once

#include <QString>

// 控制台要读的 config.yaml 定位逻辑。
//
// 控制台和 Go 后端是两个独立进程，默认各读各的 <程序目录>/config/config.yaml；
// 但用户完全可能把控制台装到别的目录（例如 E:\Program Files\私有云），这时就找不到后端配置。
// 因此没有显式 --config 时按下面的顺序自动发现：
//   1. <程序目录>/config/config.yaml（经典同目录部署，优先）
//   2. Windows 服务 FileSyncBackend（nssm 的 AppDirectory/Application/AppParameters、ImagePath）
//   3. 常见安装位置（ProgramData/Program Files 下的 syc-file、FileSync；Linux 的 /etc 等）
//   4. 都没有 → 回落到 <程序目录>/config/config.yaml（界面会提示未运行）
namespace configlocator {

// explicitPath 非空时直接返回它的绝对路径（即使文件不存在，尊重用户显式指定）。
QString resolve(const QString &explicitPath);

// 只做自动发现；找不到返回空字符串。
QString discover();

} // namespace configlocator