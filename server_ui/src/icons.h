#pragma once

#include <QColor>
#include <QIcon>
#include <QPixmap>
#include <QString>

// icons 统一提供线性图标（Windows 用 Segoe Fluent Icons / MDL2 字形，缺字体时退回系统图标），
// 以及侧边栏的品牌徽标。页面里不直接写 unicode 码点。
namespace icons {

enum class Glyph {
    Home,
    Settings,
    Document,
    Refresh,
    Play,
    Stop,
    Restart,
    Folder,
    Copy,
    External,
    Search,
    Close,
    ChevronDown,
    Info,
    Warning,
    Chip,
    Chart,
    OpenFile,
};

// pixmap 渲染单个图标（带 2x 像素密度，高分屏不糊）。
QPixmap pixmap(Glyph glyph, const QColor &color, int px = 16);

// make 生成单色图标。
QIcon make(Glyph glyph, const QColor &color, int px = 16);

// make 默认灰蓝色（#475569）。
QIcon make(Glyph glyph, int px = 16);

// dual 用于侧边栏导航：常态浅灰、选中/按下白色。
QIcon dual(Glyph glyph, int px = 16);

// appIcon 渲染应用图标（资源里的 512px PNG，按需缩放）；withStatusDot=true 时在右下角
// 叠一个状态小圆点（托盘图标用：绿=运行、红=未运行、琥珀=未知）。
QPixmap appIcon(int px, bool withStatusDot = false, bool running = false, bool known = true);

} // namespace icons