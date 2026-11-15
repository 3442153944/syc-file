#include "widgets.h"

#include <QHBoxLayout>
#include <QLabel>
#include <QPainter>
#include <QPainterPath>
#include <QPushButton>
#include <QRadialGradient>
#include <QScrollArea>
#include <QStyle>
#include <QVBoxLayout>

namespace ui {

Card::Card(QWidget *parent, Kind kind)
    : QFrame(parent)
{
    setObjectName(kind == Kind::Hero ? QStringLiteral("HeroCard") : QStringLiteral("Card"));
    setFrameShape(QFrame::NoFrame);

    outer_ = new QVBoxLayout(this);
    outer_->setContentsMargins(18, 16, 18, 16);
    outer_->setSpacing(12);

    head_ = new QWidget(this);
    head_->setSizePolicy(QSizePolicy::Preferred, QSizePolicy::Fixed);
    auto *headLayout = new QHBoxLayout(head_);
    headLayout->setContentsMargins(0, 0, 0, 0);
    headLayout->setSpacing(10);
    title_ = new QLabel(head_);
    title_->setObjectName(QStringLiteral("CardTitle"));
    subtitle_ = new QLabel(head_);
    subtitle_->setObjectName(QStringLiteral("CardSubtitle"));
    headLayout->addWidget(title_);
    headLayout->addWidget(subtitle_);
    headLayout->addStretch(1);
    outer_->addWidget(head_);
    head_->hide();

    body_ = new QVBoxLayout();
    body_->setContentsMargins(0, 0, 0, 0);
    body_->setSpacing(10);
    outer_->addLayout(body_, 1);
}

QHBoxLayout *Card::headerLayout() const
{
    return qobject_cast<QHBoxLayout *>(head_->layout());
}

void Card::setContentMargins(int left, int top, int right, int bottom)
{
    outer_->setContentsMargins(left, top, right, bottom);
}

void Card::setBodySpacing(int spacing)
{
    body_->setSpacing(spacing);
}

void Card::setTitle(const QString &title, const QString &subtitle)
{
    title_->setText(title);
    subtitle_->setText(subtitle);
    subtitle_->setVisible(!subtitle.isEmpty());
    head_->show();
}

void Card::addBodyWidget(QWidget *widget, int stretch)
{
    body_->addWidget(widget, stretch);
}

void Card::addBodyLayout(QLayout *layout, int stretch)
{
    body_->addLayout(layout, stretch);
}

StatCard::StatCard(const QString &caption, const QColor &accent, QWidget *parent)
    : QFrame(parent)
{
    setObjectName(QStringLiteral("Card"));
    setFrameShape(QFrame::NoFrame);
    setMinimumHeight(104);

    auto *row = new QHBoxLayout(this);
    row->setContentsMargins(16, 14, 16, 14);
    row->setSpacing(12);

    auto *bar = new QFrame(this);
    bar->setFixedWidth(3);
    bar->setSizePolicy(QSizePolicy::Fixed, QSizePolicy::Expanding);
    bar->setStyleSheet(QStringLiteral("background-color:%1;border-radius:1px;").arg(accent.name()));

    auto *texts = new QVBoxLayout();
    texts->setContentsMargins(0, 0, 0, 0);
    texts->setSpacing(3);

    auto *captionLabel = new QLabel(caption, this);
    captionLabel->setObjectName(QStringLiteral("StatCaption"));
    value_ = new QLabel(QStringLiteral("—"), this);
    value_->setObjectName(QStringLiteral("StatValue"));
    sub_ = new QLabel(QString(), this);
    sub_->setObjectName(QStringLiteral("StatSub"));
    sub_->setWordWrap(false);

    texts->addWidget(captionLabel);
    texts->addWidget(value_);
    texts->addWidget(sub_);
    texts->addStretch(1);

    row->addWidget(bar);
    row->addLayout(texts, 1);
}

void StatCard::setValue(const QString &value)
{
    value_->setText(value.isEmpty() ? QStringLiteral("—") : value);
}

void StatCard::setSub(const QString &sub)
{
    sub_->setText(sub);
    sub_->setVisible(!sub.isEmpty());
}

Pill::Pill(QWidget *parent)
    : QLabel(parent)
{
    setObjectName(QStringLiteral("Pill"));
    setAlignment(Qt::AlignCenter);
    setSizePolicy(QSizePolicy::Maximum, QSizePolicy::Fixed);
    setProperty("tone", QStringLiteral("neutral"));
}

Pill::Pill(const QString &text, Tone tone, QWidget *parent)
    : Pill(parent)
{
    setText(text);
    setTone(tone);
}

void Pill::setTone(Tone tone)
{
    static const char *names[] = {"neutral", "success", "danger", "warning", "accent"};
    tone_ = tone;
    setProperty("tone", QString::fromLatin1(names[static_cast<int>(tone)]));
    style()->unpolish(this);
    style()->polish(this);
    update();
}

void Pill::applyTone()
{
    setTone(tone_);
}

StatusOrb::StatusOrb(QWidget *parent)
    : QWidget(parent)
{
    setFixedSize(46, 46);
}

void StatusOrb::setState(bool running, bool warning)
{
    running_ = running;
    warning_ = warning;
    update();
}

void StatusOrb::paintEvent(QPaintEvent *)
{
    QPainter painter(this);
    painter.setRenderHint(QPainter::Antialiasing, true);

    const QColor base = warning_ ? QColor("#D97706") : (running_ ? QColor("#10B981") : QColor("#EF4444"));
    const QPointF center(width() / 2.0, height() / 2.0);

    // 外圈柔光
    QColor halo = base;
    halo.setAlpha(38);
    painter.setPen(Qt::NoPen);
    painter.setBrush(halo);
    painter.drawEllipse(center, 22.0, 22.0);

    // 实心渐变球
    QRadialGradient gradient(center - QPointF(5, 5), 26.0);
    gradient.setColorAt(0.0, base.lighter(125));
    gradient.setColorAt(1.0, base.darker(112));
    painter.setBrush(gradient);
    painter.drawEllipse(center, 14.0, 14.0);

    // 高光
    QColor highlight(255, 255, 255, 170);
    painter.setBrush(highlight);
    painter.drawEllipse(center - QPointF(4.2, 5.2), 4.4, 3.4);
}

QFrame *sectionLine(QWidget *parent)
{
    auto *line = new QFrame(parent);
    line->setObjectName(QStringLiteral("SectionLine"));
    line->setFixedHeight(1);
    line->setFrameShape(QFrame::NoFrame);
    return line;
}

QPushButton *iconButton(const QIcon &icon, const QString &tooltip, QWidget *parent)
{
    auto *button = new QPushButton(parent);
    button->setObjectName(QStringLiteral("GhostBtn"));
    button->setIcon(icon);
    button->setIconSize(QSize(16, 16));
    button->setFixedSize(28, 28);
    button->setToolTip(tooltip);
    button->setCursor(Qt::PointingHandCursor);
    return button;
}

QScrollArea *scrollPage(QWidget *content, QWidget *parent)
{
    auto *scroll = new QScrollArea(parent);
    scroll->setWidgetResizable(true);
    scroll->setFrameShape(QFrame::NoFrame);
    scroll->setWidget(content);
    scroll->setHorizontalScrollBarPolicy(Qt::ScrollBarAlwaysOff);
    scroll->viewport()->setAutoFillBackground(false);
    content->setObjectName(QStringLiteral("PageContent"));
    return scroll;
}

} // namespace ui