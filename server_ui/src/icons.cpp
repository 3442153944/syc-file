#include "icons.h"

#include <QApplication>
#include <QFont>
#include <QFontDatabase>
#include <QImage>
#include <QLinearGradient>
#include <QPainter>
#include <QPainterPath>
#include <QStyle>

namespace {

// iconFamily 只在第一次调用时查一次字体库：Win11 有 Segoe Fluent Icons，
// Win10 只有 Segoe MDL2 Assets（两者码点一致）。
const QString &iconFamily()
{
    static const QString family = []() {
        const QStringList families = QFontDatabase::families();
        for (const QString &name : {QStringLiteral("Segoe Fluent Icons"), QStringLiteral("Segoe MDL2 Assets")}) {
            if (families.contains(name))
                return name;
        }
        return QString();
    }();
    return family;
}

ushort glyphCode(icons::Glyph glyph)
{
    switch (glyph) {
    case icons::Glyph::Home:
        return 0xE80F;
    case icons::Glyph::Settings:
        return 0xE713;
    case icons::Glyph::Document:
        return 0xE8A5;
    case icons::Glyph::Refresh:
        return 0xE72C;
    case icons::Glyph::Play:
        return 0xE768;
    case icons::Glyph::Stop:
        return 0xE71A;
    case icons::Glyph::Restart:
        return 0xE895;
    case icons::Glyph::Folder:
        return 0xE8B7;
    case icons::Glyph::Copy:
        return 0xE8C8;
    case icons::Glyph::External:
        return 0xE8A7;
    case icons::Glyph::Search:
        return 0xE721;
    case icons::Glyph::Close:
        return 0xE711;
    case icons::Glyph::ChevronDown:
        return 0xE70D;
    case icons::Glyph::Info:
        return 0xE946;
    case icons::Glyph::Warning:
        return 0xE7BA;
    case icons::Glyph::Chip:
        return 0xE950;
    case icons::Glyph::Chart:
        return 0xE9D9;
    case icons::Glyph::OpenFile:
        return 0xE8E5;
    }
    return 0xE80F;
}

QStyle::StandardPixmap fallbackGlyph(icons::Glyph glyph)
{
    switch (glyph) {
    case icons::Glyph::Refresh:
    case icons::Glyph::Restart:
        return QStyle::SP_BrowserReload;
    case icons::Glyph::Play:
        return QStyle::SP_MediaPlay;
    case icons::Glyph::Stop:
        return QStyle::SP_MediaStop;
    case icons::Glyph::Folder:
        return QStyle::SP_DirOpenIcon;
    case icons::Glyph::Document:
    case icons::Glyph::OpenFile:
        return QStyle::SP_FileIcon;
    case icons::Glyph::Search:
        return QStyle::SP_FileDialogContentsView;
    default:
        return QStyle::SP_FileDialogDetailedView;
    }
}

} // namespace

namespace icons {

QPixmap pixmap(Glyph glyph, const QColor &color, int px)
{
    if (iconFamily().isEmpty())
        return QApplication::style()->standardIcon(fallbackGlyph(glyph)).pixmap(px, px);

    const qreal dpr = 2.0;
    QPixmap pm(static_cast<int>(px * dpr), static_cast<int>(px * dpr));
    pm.setDevicePixelRatio(dpr);
    pm.fill(Qt::transparent);

    QPainter painter(&pm);
    painter.setRenderHint(QPainter::Antialiasing, true);
    painter.setRenderHint(QPainter::TextAntialiasing, true);
    QFont font(iconFamily());
    font.setPixelSize(px);
    painter.setFont(font);
    painter.setPen(color);
    // MDL2 字形自带上下留白，略微放大后按矩形居中，视觉上更接近常规图标大小。
    const uint code = glyphCode(glyph);
    painter.drawText(QRect(0, 0, px, px), Qt::AlignCenter, QString::fromUcs4(&code, 1));
    painter.end();
    return pm;
}

QIcon make(Glyph glyph, const QColor &color, int px)
{
    return QIcon(pixmap(glyph, color, px));
}

QIcon make(Glyph glyph, int px)
{
    return make(glyph, QColor("#475569"), px);
}

QIcon dual(Glyph glyph, int px)
{
    QIcon icon;
    icon.addPixmap(pixmap(glyph, QColor("#94A3B8"), px), QIcon::Normal, QIcon::Off);
    icon.addPixmap(pixmap(glyph, QColor("#F8FAFC"), px), QIcon::Normal, QIcon::On);
    icon.addPixmap(pixmap(glyph, QColor("#F8FAFC"), px), QIcon::Selected, QIcon::Off);
    icon.addPixmap(pixmap(glyph, QColor("#F8FAFC"), px), QIcon::Selected, QIcon::On);
    icon.addPixmap(pixmap(glyph, QColor("#64748B"), px), QIcon::Disabled, QIcon::Off);
    return icon;
}

QPixmap appIcon(int px, bool withStatusDot, bool running, bool known)
{
    const qreal dpr = 2.0;
    QPixmap pm(qRound(px * dpr), qRound(px * dpr));
    pm.setDevicePixelRatio(dpr);
    pm.fill(Qt::transparent);

    QPainter painter(&pm);
    painter.setRenderHint(QPainter::Antialiasing, true);
    painter.setRenderHint(QPainter::SmoothPixmapTransform, true);

    static const QImage logo(QStringLiteral(":/app/icon.png"));
    if (!logo.isNull()) {
        painter.drawImage(QRectF(0, 0, px, px), logo);
    } else {
        // 资源缺失兜底：蓝色圆角块 + FS，保证托盘/侧边栏不会变成空白。
        const QRectF box(0.5, 0.5, px - 1.0, px - 1.0);
        QLinearGradient gradient(box.topLeft(), box.bottomRight());
        gradient.setColorAt(0.0, QColor("#3B82F6"));
        gradient.setColorAt(1.0, QColor("#1D4ED8"));
        QPainterPath path;
        path.addRoundedRect(box, px * 0.24, px * 0.24);
        painter.setPen(Qt::NoPen);
        painter.setBrush(gradient);
        painter.drawPath(path);
        QFont font(QStringLiteral("Segoe UI"));
        font.setPixelSize(qRound(px * 0.40));
        font.setBold(true);
        painter.setFont(font);
        painter.setPen(Qt::white);
        painter.drawText(QRectF(0, 0, px, px), Qt::AlignCenter, QStringLiteral("FS"));
    }

    if (withStatusDot) {
        const qreal dot = qMax<qreal>(6.0, px * 0.34);
        const QRectF dotRect(px - dot, px - dot, dot, dot);
        painter.setPen(QPen(QColor("#FFFFFF"), qMax<qreal>(1.2, px * 0.055)));
        painter.setBrush(known ? (running ? QColor("#10B981") : QColor("#EF4444")) : QColor("#F59E0B"));
        painter.drawEllipse(dotRect.adjusted(0.5, 0.5, -0.5, -0.5));
    }

    painter.end();
    return pm;
}

} // namespace icons