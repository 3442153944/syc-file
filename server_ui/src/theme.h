#pragma once

#include <QColor>
#include <QString>

class QApplication;

// theme 收拢整份控制台的视觉语言：调色板、字体、全局 QSS、状态色。
// 页面里只引用 theme::Color::* 常量，不散落十六进制颜色值。
namespace theme {

namespace Color {
inline const QColor PageBg("#F4F6FA");
inline const QColor Surface("#FFFFFF");
inline const QColor SurfaceAlt("#F8FAFC");
inline const QColor Border("#E4E8F0");
inline const QColor BorderStrong("#D5DCE8");
inline const QColor Text("#0F172A");
inline const QColor TextSecondary("#475569");
inline const QColor TextMuted("#94A3B8");
inline const QColor Accent("#2563EB");
inline const QColor AccentSoft("#EFF6FF");
inline const QColor Success("#059669");
inline const QColor SuccessSoft("#ECFDF5");
inline const QColor Danger("#DC2626");
inline const QColor DangerSoft("#FEF2F2");
inline const QColor Warning("#D97706");
inline const QColor WarningSoft("#FFFBEB");
inline const QColor Sidebar("#0F172A");
inline const QColor Mono("#334155");
} // namespace Color

// apply 安装 Fusion 风格 + 调色板 + 全局 QSS + 默认字体（微软雅黑 9.75pt）。
void apply(QApplication &app);

// accentColor / statusColor 供自绘控件（状态圆点、指标卡强调条）取色。
QColor accentColor();
QColor statusColor(bool ok);

} // namespace theme