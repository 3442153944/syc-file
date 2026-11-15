#include "theme.h"

#include <QApplication>
#include <QFont>
#include <QFontDatabase>
#include <QPainter>
#include <QPainterPath>
#include <QPalette>
#include <QProxyStyle>
#include <QStyleOption>
#include <QStyleFactory>

namespace {

// ConsoleStyle 只接管三处系统控件自绘，让它们和 QSS 的圆角/配色一致：
// 复选框、单选按钮、下拉箭头。其余全部交给 Fusion + QSS。
class ConsoleStyle : public QProxyStyle {
public:
    using QProxyStyle::QProxyStyle;

    void drawPrimitive(PrimitiveElement element, const QStyleOption *option, QPainter *painter,
                       const QWidget *widget) const override
    {
        switch (element) {
        case PE_IndicatorCheckBox:
            drawCheck(option, painter, false);
            return;
        case PE_IndicatorRadioButton:
            drawCheck(option, painter, true);
            return;
        case PE_IndicatorArrowDown:
            drawChevron(option, painter);
            return;
        default:
            break;
        }
        QProxyStyle::drawPrimitive(element, option, painter, widget);
    }

private:
    static void drawCheck(const QStyleOption *option, QPainter *painter, bool radio)
    {
        if (!option || !painter)
            return;
        const bool on = option->state & State_On;
        const bool hover = option->state & State_MouseOver;
        const bool enabled = option->state & State_Enabled;

        QRectF box = option->rect;
        const qreal side = qMin<qreal>(16.0, qMin(box.width(), box.height()));
        box = QRectF(box.center().x() - side / 2.0, box.center().y() - side / 2.0, side, side);

        painter->save();
        painter->setRenderHint(QPainter::Antialiasing, true);

        QColor border = enabled ? QColor("#C3CCDA") : QColor("#E2E7F0");
        if (hover)
            border = QColor("#94A3B8");
        if (on)
            border = QColor("#2563EB");

        if (radio) {
            painter->setPen(QPen(border, 1.2));
            painter->setBrush(on ? QColor("#2563EB") : QColor("#FFFFFF"));
            painter->drawEllipse(box.adjusted(0.6, 0.6, -0.6, -0.6));
            if (on) {
                painter->setPen(Qt::NoPen);
                painter->setBrush(QColor("#FFFFFF"));
                painter->drawEllipse(box.adjusted(4.4, 4.4, -4.4, -4.4));
            }
        } else {
            QPainterPath path;
            path.addRoundedRect(box.adjusted(0.6, 0.6, -0.6, -0.6), 4.0, 4.0);
            painter->setPen(QPen(border, 1.2));
            painter->setBrush(on ? QColor("#2563EB") : QColor("#FFFFFF"));
            painter->drawPath(path);
            if (on) {
                QPainterPath tick;
                tick.moveTo(box.left() + 4.2, box.top() + 8.2);
                tick.lineTo(box.left() + 7.0, box.top() + 11.0);
                tick.lineTo(box.left() + 11.8, box.top() + 5.4);
                painter->setPen(QPen(QColor("#FFFFFF"), 1.9, Qt::SolidLine, Qt::RoundCap, Qt::RoundJoin));
                painter->setBrush(Qt::NoBrush);
                painter->drawPath(tick);
            }
        }
        painter->restore();
    }

    static void drawChevron(const QStyleOption *option, QPainter *painter)
    {
        if (!option || !painter)
            return;
        const QRect r = option->rect;
        const QPointF center = QRectF(r).center();
        const qreal w = 8.0;
        const qreal h = 4.6;

        QPainterPath path;
        path.moveTo(center.x() - w / 2.0, center.y() - h / 2.0);
        path.lineTo(center.x(), center.y() + h / 2.0);
        path.lineTo(center.x() + w / 2.0, center.y() - h / 2.0);

        painter->save();
        painter->setRenderHint(QPainter::Antialiasing, true);
        QColor color = option->state & State_Enabled ? QColor("#64748B") : QColor("#A8B1C2");
        if (option->state & State_MouseOver)
            color = QColor("#334155");
        painter->setPen(QPen(color, 1.6, Qt::SolidLine, Qt::RoundCap, Qt::RoundJoin));
        painter->setBrush(Qt::NoBrush);
        painter->drawPath(path);
        painter->restore();
    }
};

QString styleSheet()
{
    return QStringLiteral(R"QSS(
QMainWindow, QWidget#AppRoot, QWidget#Page, QStackedWidget, QWidget#PageContent {
    background-color: #F4F6FA;
}

/* ---------- 侧边栏 ---------- */
QWidget#Sidebar {
    background-color: #0F172A;
}
QLabel#BrandTitle {
    color: #F8FAFC;
    font-size: 15px;
    font-weight: bold;
}
QLabel#BrandSub {
    color: #64748B;
    font-size: 11px;
}
QPushButton#NavButton {
    background-color: transparent;
    color: #94A3B8;
    border: none;
    border-radius: 8px;
    padding: 0 12px;
    min-height: 38px;
    font-size: 13px;
    text-align: left;
}
QPushButton#NavButton:hover {
    background-color: #1E293B;
    color: #E2E8F0;
}
QPushButton#NavButton:checked {
    background-color: rgba(59, 130, 246, 0.18);
    color: #FFFFFF;
    font-weight: bold;
}
QLabel#SidebarFooter {
    color: #64748B;
    font-size: 11px;
}
QLabel#SidebarState {
    color: #94A3B8;
    font-size: 11px;
}
QFrame#SidebarLine {
    background-color: #1E293B;
    border: none;
    max-height: 1px;
    min-height: 1px;
}

/* ---------- 顶栏 ---------- */
QWidget#Header {
    background-color: #FFFFFF;
    border-bottom: 1px solid #E4E8F0;
}
QLabel#PageTitle {
    color: #0F172A;
    font-size: 19px;
    font-weight: bold;
}
QLabel#PageSubtitle {
    color: #64748B;
    font-size: 12px;
}
QLabel#StatusChip {
    border-radius: 14px;
    padding: 5px 12px;
    font-size: 12px;
    font-weight: bold;
}
QLabel#StatusChip[state="running"] {
    background-color: #ECFDF5;
    color: #047857;
}
QLabel#StatusChip[state="stopped"] {
    background-color: #FEF2F2;
    color: #B91C1C;
}
QLabel#StatusChip[state="warning"] {
    background-color: #FFFBEB;
    color: #B45309;
}

/* ---------- 按钮 ---------- */
QPushButton {
    background-color: #FFFFFF;
    color: #334155;
    border: 1px solid #D8DEE9;
    border-radius: 8px;
    padding: 0 14px;
    min-height: 32px;
    font-size: 13px;
}
QPushButton:hover {
    background-color: #F8FAFC;
    border-color: #C3CCDA;
}
QPushButton:pressed {
    background-color: #F1F5F9;
}
QPushButton:disabled {
    color: #A8B1C2;
    background-color: #F8FAFC;
    border-color: #E8ECF3;
}
QPushButton#Primary {
    background-color: #2563EB;
    border: 1px solid #2563EB;
    color: #FFFFFF;
    font-weight: bold;
}
QPushButton#Primary:hover {
    background-color: #1D4ED8;
    border-color: #1D4ED8;
}
QPushButton#Primary:pressed {
    background-color: #1E40AF;
}
QPushButton#Primary:disabled {
    background-color: #BFD3F8;
    border-color: #BFD3F8;
    color: #F8FAFC;
}
QPushButton#DangerBtn {
    color: #DC2626;
    border-color: #F6C9C9;
}
QPushButton#DangerBtn:hover {
    background-color: #FEF2F2;
    border-color: #FCA5A5;
}
QPushButton#DangerBtn:disabled {
    color: #E5A3A3;
    border-color: #F3DDDD;
    background-color: #FFFFFF;
}
QPushButton#GhostBtn {
    background-color: transparent;
    border: 1px solid transparent;
    color: #64748B;
    padding: 0 8px;
    min-height: 28px;
}
QPushButton#GhostBtn:hover {
    background-color: #F1F5F9;
    color: #334155;
}
QPushButton#FilterPill {
    background-color: #F1F5F9;
    color: #64748B;
    border: 1px solid transparent;
    border-radius: 13px;
    padding: 0 12px;
    min-height: 26px;
    max-height: 26px;
    font-size: 12px;
}
QPushButton#FilterPill:hover {
    background-color: #E8EDF5;
    color: #334155;
}
QPushButton#FilterPill:checked {
    background-color: #EFF6FF;
    color: #1D4ED8;
    border-color: #BFDBFE;
    font-weight: bold;
}

/* ---------- 输入控件 ---------- */
QLineEdit, QComboBox, QPlainTextEdit, QTextEdit {
    background-color: #FFFFFF;
    color: #0F172A;
    border: 1px solid #D8DEE9;
    border-radius: 8px;
    padding: 0 10px;
    min-height: 32px;
    font-size: 13px;
    selection-background-color: #BFDBFE;
    selection-color: #0F172A;
}
QLineEdit:hover, QComboBox:hover {
    border-color: #C3CCDA;
}
QLineEdit:focus, QComboBox:focus, QPlainTextEdit:focus, QTextEdit:focus {
    border: 1px solid #2563EB;
}
QLineEdit:disabled, QComboBox:disabled, QPlainTextEdit:disabled {
    color: #A8B1C2;
    background-color: #F8FAFC;
    border-color: #E8ECF3;
}
QComboBox {
    padding-right: 26px;
}
QComboBox::drop-down {
    subcontrol-origin: padding;
    subcontrol-position: center right;
    width: 26px;
    border: none;
    background-color: transparent;
}
QComboBox QAbstractItemView {
    background-color: #FFFFFF;
    border: 1px solid #D8DEE9;
    border-radius: 8px;
    padding: 4px;
    outline: 0;
    selection-background-color: #EFF6FF;
    selection-color: #1D4ED8;
}
QPlainTextEdit {
    padding: 6px 10px;
}
QPlainTextEdit#ConfigText {
    background-color: transparent;
    border: none;
    padding: 0;
    font-family: "Cascadia Mono", "Consolas", "Microsoft YaHei UI";
    font-size: 12px;
}
QPlainTextEdit#LogView {
    background-color: #0B1220;
    color: #CBD5E1;
    border: none;
    border-top-left-radius: 0;
    border-top-right-radius: 0;
    border-bottom-left-radius: 11px;
    border-bottom-right-radius: 11px;
    padding: 10px 14px;
    font-family: "Cascadia Mono", "Consolas", "Microsoft YaHei UI";
    font-size: 12px;
    selection-background-color: #1E3A8A;
    selection-color: #F8FAFC;
}
QCheckBox {
    color: #334155;
    spacing: 8px;
    font-size: 13px;
}
QCheckBox:disabled {
    color: #A8B1C2;
}

/* ---------- 卡片 ---------- */
QFrame#Card {
    background-color: #FFFFFF;
    border: 1px solid #E4E8F0;
    border-radius: 12px;
}
QFrame#HeroCard {
    background-color: qlineargradient(x1:0, y1:0, x2:1, y2:0, stop:0 #EFF6FF, stop:0.55 #FFFFFF, stop:1 #FFFFFF);
    border: 1px solid #DBE7FB;
    border-radius: 12px;
}
QFrame#SectionLine {
    background-color: #F1F5F9;
    border: none;
    max-height: 1px;
    min-height: 1px;
}
QLabel#CardTitle {
    color: #0F172A;
    font-size: 14px;
    font-weight: bold;
}
QLabel#CardSubtitle {
    color: #94A3B8;
    font-size: 11px;
}
QLabel#HeroTitle {
    color: #0F172A;
    font-size: 20px;
    font-weight: bold;
}
QLabel#HeroSub {
    color: #64748B;
    font-size: 12px;
}
QLabel#StatCaption {
    color: #64748B;
    font-size: 12px;
}
QLabel#StatValue {
    color: #0F172A;
    font-size: 20px;
    font-weight: bold;
}
QLabel#StatSub {
    color: #94A3B8;
    font-size: 11px;
}
QLabel#Key {
    color: #64748B;
    font-size: 12px;
}
QLabel#FieldLabel {
    color: #0F172A;
    font-size: 13px;
    font-weight: bold;
}
QLabel#Value {
    color: #0F172A;
    font-size: 13px;
}
QLabel#Muted {
    color: #64748B;
    font-size: 12px;
}
QLabel#Hint {
    color: #94A3B8;
    font-size: 11px;
}
QLabel#PathValue {
    color: #334155;
    font-size: 12px;
    font-family: "Cascadia Mono", "Consolas", "Microsoft YaHei UI";
}
QLabel#Pill {
    border-radius: 9px;
    padding: 2px 8px;
    font-size: 11px;
    font-weight: bold;
}
QLabel#Pill[tone="neutral"] {
    background-color: #F1F5F9;
    color: #475569;
}
QLabel#Pill[tone="success"] {
    background-color: #ECFDF5;
    color: #047857;
}
QLabel#Pill[tone="danger"] {
    background-color: #FEF2F2;
    color: #B91C1C;
}
QLabel#Pill[tone="warning"] {
    background-color: #FFFBEB;
    color: #B45309;
}
QLabel#Pill[tone="accent"] {
    background-color: #EFF6FF;
    color: #1D4ED8;
}

/* ---------- 页签 ---------- */
QTabWidget::pane {
    border: none;
    background-color: transparent;
}
QTabBar {
    background-color: transparent;
}
QTabBar::tab {
    background-color: transparent;
    color: #64748B;
    padding: 7px 16px;
    margin-right: 4px;
    border: none;
    border-bottom: 2px solid transparent;
    font-size: 13px;
}
QTabBar::tab:hover {
    color: #334155;
}
QTabBar::tab:selected {
    color: #2563EB;
    border-bottom: 2px solid #2563EB;
    font-weight: bold;
}

/* ---------- 滚动条 / 滚动区 ---------- */
QScrollArea {
    background-color: transparent;
    border: none;
}
QScrollBar:vertical {
    background-color: transparent;
    width: 10px;
    margin: 2px;
}
QScrollBar::handle:vertical {
    background-color: #CBD5E1;
    border-radius: 3px;
    min-height: 36px;
}
QScrollBar::handle:vertical:hover {
    background-color: #94A3B8;
}
QScrollBar::add-line:vertical, QScrollBar::sub-line:vertical {
    height: 0;
    border: none;
    background: none;
}
QScrollBar::add-page:vertical, QScrollBar::sub-page:vertical {
    background: transparent;
}
QScrollBar:horizontal {
    background-color: transparent;
    height: 10px;
    margin: 2px;
}
QScrollBar::handle:horizontal {
    background-color: #CBD5E1;
    border-radius: 3px;
    min-width: 36px;
}
QScrollBar::handle:horizontal:hover {
    background-color: #94A3B8;
}
QScrollBar::add-line:horizontal, QScrollBar::sub-line:horizontal {
    width: 0;
    border: none;
    background: none;
}
QScrollBar::add-page:horizontal, QScrollBar::sub-page:horizontal {
    background: transparent;
}

/* ---------- 状态栏 / 工具提示 / 菜单 / 弹窗 ---------- */
QStatusBar {
    background-color: #FFFFFF;
    border-top: 1px solid #E4E8F0;
    color: #64748B;
    font-size: 12px;
}
QStatusBar::item {
    border: none;
}
QStatusBar QLabel {
    color: #64748B;
    font-size: 12px;
}
QToolTip {
    background-color: #0F172A;
    color: #F8FAFC;
    border: none;
    padding: 6px 8px;
    font-size: 12px;
}
QMenu {
    background-color: #FFFFFF;
    border: 1px solid #E4E8F0;
    border-radius: 10px;
    padding: 6px;
}
QMenu::item {
    padding: 7px 18px;
    border-radius: 6px;
    color: #334155;
    font-size: 13px;
}
QMenu::item:selected {
    background-color: #EFF6FF;
    color: #1D4ED8;
}
QMenu::separator {
    height: 1px;
    background-color: #E9EDF3;
    margin: 5px 8px;
}
QMessageBox {
    background-color: #FFFFFF;
}
QMessageBox QLabel {
    color: #334155;
    font-size: 13px;
}

/* 深色日志面板内部的滚动条：必须放在全局浅色滚动条之后，靠规则顺序覆盖。 */
QPlainTextEdit#LogView QScrollBar:vertical {
    background: transparent;
    width: 10px;
    margin: 2px;
}
QPlainTextEdit#LogView QScrollBar::handle:vertical {
    background-color: #334155;
    border-radius: 3px;
    min-height: 30px;
}
QPlainTextEdit#LogView QScrollBar::handle:vertical:hover {
    background-color: #475569;
}
QPlainTextEdit#LogView QScrollBar::add-line:vertical,
QPlainTextEdit#LogView QScrollBar::sub-line:vertical {
    height: 0;
    border: none;
    background: none;
}
QPlainTextEdit#LogView QScrollBar::add-page:vertical,
QPlainTextEdit#LogView QScrollBar::sub-page:vertical {
    background: transparent;
}
QPlainTextEdit#LogView QScrollBar:horizontal {
    background: transparent;
    height: 10px;
    margin: 2px;
}
QPlainTextEdit#LogView QScrollBar::handle:horizontal {
    background-color: #334155;
    border-radius: 3px;
    min-width: 30px;
}
QPlainTextEdit#LogView QScrollBar::handle:horizontal:hover {
    background-color: #475569;
}
QPlainTextEdit#LogView QScrollBar::add-line:horizontal,
QPlainTextEdit#LogView QScrollBar::sub-line:horizontal {
    width: 0;
    border: none;
    background: none;
}
QPlainTextEdit#LogView QScrollBar::add-page:horizontal,
QPlainTextEdit#LogView QScrollBar::sub-page:horizontal {
    background: transparent;
}
)QSS");
}

} // namespace

namespace theme {

void apply(QApplication &app)
{
    QApplication::setStyle(new ConsoleStyle(QStyleFactory::create(QStringLiteral("Fusion"))));

    QFont font;
    font.setFamilies({QStringLiteral("Microsoft YaHei UI"), QStringLiteral("Microsoft YaHei"),
                      QStringLiteral("Segoe UI")});
    font.setPointSizeF(9.75);
    QApplication::setFont(font);

    QPalette palette;
    palette.setColor(QPalette::Window, Color::PageBg);
    palette.setColor(QPalette::WindowText, Color::Text);
    palette.setColor(QPalette::Base, Color::Surface);
    palette.setColor(QPalette::AlternateBase, Color::SurfaceAlt);
    palette.setColor(QPalette::Text, Color::Text);
    palette.setColor(QPalette::Button, Color::Surface);
    palette.setColor(QPalette::ButtonText, Color::TextSecondary);
    palette.setColor(QPalette::Highlight, Color::Accent);
    palette.setColor(QPalette::HighlightedText, Qt::white);
    palette.setColor(QPalette::PlaceholderText, Color::TextMuted);
    palette.setColor(QPalette::ToolTipBase, Color::Sidebar);
    palette.setColor(QPalette::ToolTipText, QColor("#F8FAFC"));
    palette.setColor(QPalette::Disabled, QPalette::Text, QColor("#A8B1C2"));
    palette.setColor(QPalette::Disabled, QPalette::WindowText, QColor("#A8B1C2"));
    palette.setColor(QPalette::Disabled, QPalette::ButtonText, QColor("#A8B1C2"));
    QApplication::setPalette(palette);

    app.setStyleSheet(styleSheet());
}

QColor accentColor()
{
    return Color::Accent;
}

QColor statusColor(bool ok)
{
    return ok ? Color::Success : Color::Danger;
}

} // namespace theme