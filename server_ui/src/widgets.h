#pragma once

#include <QFrame>
#include <QLabel>
#include <QWidget>

class QHBoxLayout;
class QPushButton;
class QScrollArea;
class QVBoxLayout;

// ui 放页面通用的「视觉积木」：卡片、指标卡、徽标、状态圆点，避免每页重复摆样式。
namespace ui {

// Card 是标准白底圆角卡片；可以带标题行，标题行右侧留给页面加操作按钮。
class Card : public QFrame {
    Q_OBJECT

public:
    enum class Kind { Plain, Hero };

    explicit Card(QWidget *parent = nullptr, Kind kind = Kind::Plain);

    QVBoxLayout *body() const { return body_; }
    QHBoxLayout *headerLayout() const;
    // setContentMargins 让整块卡片贴边（日志视图需要铺满卡片内侧）。
    void setContentMargins(int left, int top, int right, int bottom);
    void setBodySpacing(int spacing);
    void setTitle(const QString &title, const QString &subtitle = QString());
    void addBodyWidget(QWidget *widget, int stretch = 0);
    void addBodyLayout(QLayout *layout, int stretch = 0);

private:
    QVBoxLayout *outer_ = nullptr;
    QVBoxLayout *body_ = nullptr;
    QWidget *head_ = nullptr;
    QLabel *title_ = nullptr;
    QLabel *subtitle_ = nullptr;
};

// StatCard 是概览页的一张小指标卡：左侧彩色强调条 + 标题 + 大号数值 + 脚注。
class StatCard : public QFrame {
    Q_OBJECT

public:
    explicit StatCard(const QString &caption, const QColor &accent, QWidget *parent = nullptr);

    void setValue(const QString &value);
    void setSub(const QString &sub);

private:
    QLabel *value_ = nullptr;
    QLabel *sub_ = nullptr;
};

// Pill 是行内小徽标（即时生效 / 需重启 / 数据库正常…），tone 决定配色。
class Pill : public QLabel {
    Q_OBJECT

public:
    enum Tone { Neutral, Success, Danger, Warning, Accent };

    explicit Pill(QWidget *parent = nullptr);
    Pill(const QString &text, Tone tone, QWidget *parent = nullptr);

    void setTone(Tone tone);
    Tone tone() const { return tone_; }

private:
    void applyTone();

    Tone tone_ = Neutral;
};

// StatusOrb 是概览页左上角的状态指示灯（带光晕的圆点），自绘以便贴合主题。
class StatusOrb : public QWidget {
    Q_OBJECT

public:
    explicit StatusOrb(QWidget *parent = nullptr);

    void setState(bool running, bool warning = false);

protected:
    void paintEvent(QPaintEvent *event) override;

private:
    bool running_ = false;
    bool warning_ = false;
};

// sectionLine 是卡片内部的行分隔线。
QFrame *sectionLine(QWidget *parent = nullptr);

// iconButton 生成一个无边框的图标按钮（复制、打开目录这类轻操作）。
QPushButton *iconButton(const QIcon &icon, const QString &tooltip, QWidget *parent);

// scrollPage 把内容页包进透明背景的滚动区（横向不出滚动条）。
QScrollArea *scrollPage(QWidget *content, QWidget *parent = nullptr);

} // namespace ui