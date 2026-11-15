#pragma once

#include <QString>

// 应用对外名称：窗口标题、托盘、开始菜单快捷方式、版本信息保持一致，避免多处手写漂移。
namespace appinfo {

inline QString displayName()
{
    return QStringLiteral("私有云服务器设置");
}

inline QString subtitle()
{
    return QStringLiteral("服务端控制台");
}

inline QString vendorName()
{
    return QStringLiteral("FileSync");
}

} // namespace appinfo