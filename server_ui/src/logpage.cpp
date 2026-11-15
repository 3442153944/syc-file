#include "logpage.h"

#include "backendclient.h"
#include "icons.h"
#include "theme.h"
#include "widgets.h"

#include <QButtonGroup>
#include <QCheckBox>
#include <QComboBox>
#include <QDesktopServices>
#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QHBoxLayout>
#include <QLabel>
#include <QLineEdit>
#include <QPlainTextEdit>
#include <QPushButton>
#include <QRegularExpression>
#include <QScrollArea>
#include <QScrollBar>
#include <QTextCursor>
#include <QTimer>
#include <QUrl>
#include <QVBoxLayout>

namespace {

// logScrollBarStyle 给深色日志面板配一套深色滚动条：按控件级样式表设置，
// 比在全局 QSS 里写后代选择器更可靠（实测全局规则会先命中）。
const char *kLogScrollBarStyle =
    "QScrollBar:vertical { background: transparent; width: 10px; margin: 2px; }"
    "QScrollBar::handle:vertical { background-color: #334155; border-radius: 3px; min-height: 30px; }"
    "QScrollBar::handle:vertical:hover { background-color: #475569; }"
    "QScrollBar::add-line:vertical, QScrollBar::sub-line:vertical { height: 0; border: none; background: none; }"
    "QScrollBar::add-page:vertical, QScrollBar::sub-page:vertical { background: transparent; }"
    "QScrollBar:horizontal { background: transparent; height: 10px; margin: 2px; }"
    "QScrollBar::handle:horizontal { background-color: #334155; border-radius: 3px; min-width: 30px; }"
    "QScrollBar::handle:horizontal:hover { background-color: #475569; }"
    "QScrollBar::add-line:horizontal, QScrollBar::sub-line:horizontal { width: 0; border: none; background: none; }"
    "QScrollBar::add-page:horizontal, QScrollBar::sub-page:horizontal { background: transparent; }";

// padField 按字符数补齐（等宽字体下即列对齐），超长截断加省略号。
QString padField(const QString &text, int width)
{
    QString value = text;
    if (value.size() > width)
        value = value.left(width - 1) + QChar(0x2026);
    while (value.size() < width)
        value.append(QLatin1Char(' '));
    return value;
}

// toFieldHtml 把一段纯文本转成 HTML 片段：转义后空格换 &nbsp;（多空格才不会被吞）。
QString toFieldHtml(const QString &text)
{
    QString html = logutil::escapeHtml(text);
    html.replace(QLatin1Char(' '), QStringLiteral("&nbsp;"));
    return html;
}

// darkLevelColor 是深色日志面板上的级别配色，比亮色主题的对比度更高。
QString darkLevelColor(const QString &level)
{
    if (level.compare(QStringLiteral("error"), Qt::CaseInsensitive) == 0)
        return QStringLiteral("#F87171");
    if (level.compare(QStringLiteral("warn"), Qt::CaseInsensitive) == 0)
        return QStringLiteral("#FBBF24");
    if (level.compare(QStringLiteral("debug"), Qt::CaseInsensitive) == 0)
        return QStringLiteral("#94A3B8");
    return QStringLiteral("#60A5FA");
}

// highlightHtml 在已转义的 HTML 文本里给关键词套一层高亮底色。
QString highlightHtml(const QString &html, const QString &keyword)
{
    if (keyword.isEmpty() || html.isEmpty())
        return html;
    QString needle = logutil::escapeHtml(keyword.trimmed());
    needle.replace(QLatin1Char(' '), QStringLiteral("&nbsp;"));
    if (needle.isEmpty())
        return html;

    const QRegularExpression pattern(QRegularExpression::escape(needle),
                                     QRegularExpression::CaseInsensitiveOption);
    QString out;
    qsizetype pos = 0;
    auto it = pattern.globalMatch(html);
    while (it.hasNext()) {
        const QRegularExpressionMatch match = it.next();
        out += html.mid(pos, match.capturedStart() - pos);
        out += QStringLiteral("<span style=\"background-color:#FDE68A;color:#7C2D12\">%1</span>")
                   .arg(match.captured());
        pos = match.capturedEnd();
    }
    out += html.mid(pos);
    return out;
}

} // namespace

LogPage::LogPage(BackendClient *client, QWidget *parent)
    : QWidget(parent), client_(client)
{
    setObjectName(QStringLiteral("Page"));

    auto *content = new QWidget;
    auto *outer = new QVBoxLayout(content);
    outer->setContentsMargins(24, 20, 24, 24);
    outer->setSpacing(16);

    auto *card = new ui::Card(this);
    card->setContentMargins(1, 1, 1, 1);
    card->setBodySpacing(0);

    // ---- 第一行：文件选择 + 跟随 ----
    auto *head = new QWidget(card);
    auto *headLayout = new QHBoxLayout(head);
    headLayout->setContentsMargins(16, 12, 16, 8);
    headLayout->setSpacing(8);

    auto *fileKey = new QLabel(QStringLiteral("日志文件"), head);
    fileKey->setObjectName(QStringLiteral("Key"));
    fileCombo_ = new QComboBox(head);
    fileCombo_->setMinimumWidth(280);
    fileCombo_->setSizePolicy(QSizePolicy::Expanding, QSizePolicy::Fixed);
    auto *refreshButton = ui::iconButton(icons::make(icons::Glyph::Refresh, theme::Color::TextSecondary),
                                         QStringLiteral("重新扫描日志目录"), head);
    auto *openDirButton = new QPushButton(icons::make(icons::Glyph::Folder, 16), QStringLiteral("打开目录"), head);
    openDirButton->setCursor(Qt::PointingHandCursor);
    countLabel_ = new QLabel(head);
    countLabel_->setObjectName(QStringLiteral("Hint"));
    followCheck_ = new QCheckBox(QStringLiteral("实时跟随"), head);

    headLayout->addWidget(fileKey);
    headLayout->addWidget(fileCombo_, 1);
    headLayout->addWidget(refreshButton);
    headLayout->addWidget(openDirButton);
    headLayout->addSpacing(8);
    headLayout->addWidget(countLabel_);
    headLayout->addWidget(followCheck_);

    // ---- 第二行：级别胶囊 + 关键词 + 行数 ----
    auto *filters = new QWidget(card);
    auto *filterLayout = new QHBoxLayout(filters);
    filterLayout->setContentsMargins(16, 4, 16, 12);
    filterLayout->setSpacing(8);

    levelGroup_ = new QButtonGroup(this);
    levelGroup_->setExclusive(true);
    addLevelPill(filterLayout, QStringLiteral("全部"), 0);
    addLevelPill(filterLayout, QStringLiteral("DEBUG"), 1);
    addLevelPill(filterLayout, QStringLiteral("INFO"), 2);
    addLevelPill(filterLayout, QStringLiteral("WARN"), 3);
    addLevelPill(filterLayout, QStringLiteral("ERROR"), 4);
    filterLayout->addStretch(1);

    auto *keywordKey = new QLabel(QStringLiteral("关键词"), filters);
    keywordKey->setObjectName(QStringLiteral("Key"));
    keywordEdit_ = new QLineEdit(filters);
    keywordEdit_->setPlaceholderText(QStringLiteral("搜索消息或调用位置（回车查询）"));
    keywordEdit_->setClearButtonEnabled(true);
    keywordEdit_->setMinimumWidth(220);
    keywordEdit_->setMaximumWidth(360);
    auto *queryButton = new QPushButton(QStringLiteral("查询"), filters);
    queryButton->setObjectName(QStringLiteral("Primary"));
    queryButton->setCursor(Qt::PointingHandCursor);

    auto *linesKey = new QLabel(QStringLiteral("行数"), filters);
    linesKey->setObjectName(QStringLiteral("Key"));
    linesCombo_ = new QComboBox(filters);
    linesCombo_->addItems({QStringLiteral("200"), QStringLiteral("500"), QStringLiteral("1000"),
                           QStringLiteral("2000")});
    linesCombo_->setCurrentText(QStringLiteral("500"));
    linesCombo_->setFixedWidth(88);

    filterLayout->addWidget(keywordKey);
    filterLayout->addWidget(keywordEdit_, 1);
    filterLayout->addWidget(queryButton);
    filterLayout->addSpacing(10);
    filterLayout->addWidget(linesKey);
    filterLayout->addWidget(linesCombo_);

    // ---- 日志正文 ----
    view_ = new QPlainTextEdit(card);
    view_->setObjectName(QStringLiteral("LogView"));
    view_->setStyleSheet(QString::fromLatin1(kLogScrollBarStyle));
    view_->setReadOnly(true);
    view_->setLineWrapMode(QPlainTextEdit::NoWrap);
    view_->setMaximumBlockCount(20000);
    view_->setPlaceholderText(QStringLiteral("选择左侧日志文件后显示最近日志"));
    view_->setSizePolicy(QSizePolicy::Expanding, QSizePolicy::Expanding);

    card->addBodyWidget(head);
    card->addBodyWidget(filters);
    card->addBodyWidget(view_, 1);
    outer->addWidget(card, 1);
    outer->addStretch(0);

    auto *pageLayout = new QVBoxLayout(this);
    pageLayout->setContentsMargins(0, 0, 0, 0);
    pageLayout->addWidget(ui::scrollPage(content, this));
    // 日志页自身不需要整页滚动，让日志视图吃掉全部高度。
    outer->setStretchFactor(card, 1);

    followTimer_ = new QTimer(this);
    followTimer_->setInterval(1000);
    connect(followTimer_, &QTimer::timeout, this, &LogPage::followTick);

    connect(refreshButton, &QPushButton::clicked, this, &LogPage::reloadFiles);
    connect(openDirButton, &QPushButton::clicked, this, &LogPage::openLogDir);
    connect(queryButton, &QPushButton::clicked, this, &LogPage::reloadContent);
    connect(fileCombo_, &QComboBox::currentIndexChanged, this, [this]() {
        pendingBytes_.clear();
        reloadContent();
    });
    connect(linesCombo_, &QComboBox::currentIndexChanged, this, &LogPage::reloadContent);
    connect(keywordEdit_, &QLineEdit::returnPressed, this, &LogPage::reloadContent);
    connect(levelGroup_, &QButtonGroup::idClicked, this, [this](int id) {
        levelThreshold_ = id;
        reloadContent();
    });
    connect(followCheck_, &QCheckBox::toggled, this, [this](bool checked) {
        if (checked) {
            reloadContent();
            followTimer_->start();
        } else {
            followTimer_->stop();
        }
    });

    reloadFiles();
}

QPushButton *LogPage::addLevelPill(QHBoxLayout *layout, const QString &text, int threshold)
{
    auto *pill = new QPushButton(text, this);
    pill->setObjectName(QStringLiteral("FilterPill"));
    pill->setCheckable(true);
    pill->setCursor(Qt::PointingHandCursor);
    pill->setToolTip(threshold == 0 ? QStringLiteral("显示所有级别")
                                    : QStringLiteral("显示 %1 及以上级别").arg(text));
    levelGroup_->addButton(pill, threshold);
    if (threshold == 0)
        pill->setChecked(true);
    layout->addWidget(pill);
    return pill;
}

void LogPage::setStatus(const BackendStatus &status)
{
    const QString dir = status.valid ? QDir::cleanPath(status.logDir) : QString();
    if (dir == logDirOverride_)
        return;
    logDirOverride_ = dir;
    reloadFiles();
}

QString LogPage::logDir() const
{
    if (!logDirOverride_.isEmpty())
        return QDir::cleanPath(logDirOverride_);
    return client_->logDir(BackendStatus{});
}

QString LogPage::currentFile() const
{
    return fileCombo_ ? fileCombo_->currentData().toString() : QString();
}

void LogPage::reloadFiles()
{
    const QString dir = logDir();
    const QString current = currentFile();
    const QStringList files = logutil::listLogFiles(dir);

    fileCombo_->blockSignals(true);
    fileCombo_->clear();
    for (const QString &file : files)
        fileCombo_->addItem(QDir(dir).relativeFilePath(file), file);
    if (files.isEmpty())
        fileCombo_->addItem(QStringLiteral("（未找到日志文件：%1）").arg(dir), QString());
    const int index = fileCombo_->findData(current);
    if (index >= 0)
        fileCombo_->setCurrentIndex(index);
    fileCombo_->blockSignals(false);

    pendingBytes_.clear();
    reloadContent();
}

void LogPage::reloadContent()
{
    const QString path = currentFile();
    view_->clear();
    followOffset_ = 0;
    pendingBytes_.clear();
    if (path.isEmpty()) {
        view_->appendHtml(QStringLiteral("<span style=\"color:#7C8CA6\">日志目录里没有 .log 文件：%1</span>")
                              .arg(logutil::escapeHtml(logDir())));
        countLabel_->clear();
        return;
    }

    const int maxLines = linesCombo_->currentText().toInt();
    const QStringList rawLines = logutil::readTailLines(path, maxLines * 2);
    QVector<LogLine> lines;
    lines.reserve(rawLines.size());
    for (const QString &raw : rawLines) {
        const LogLine line = logutil::parseLine(raw);
        if (matchLevel(line) && matchKeyword(line))
            lines.append(line);
    }
    while (lines.size() > maxLines)
        lines.removeFirst();
    appendLines(lines);
    displayedLines_ = lines.size();
    followOffset_ = QFileInfo(path).size();
    view_->moveCursor(QTextCursor::End);
    // 布局更新后滚动条最大值才准，下一轮事件循环再贴到底部。
    QTimer::singleShot(0, view_, [this]() {
        auto *bar = view_->verticalScrollBar();
        bar->setValue(bar->maximum());
    });
    updateCountLabel();
}

void LogPage::followTick()
{
    const QString path = currentFile();
    if (path.isEmpty() || !followCheck_->isChecked())
        return;
    QFile file(path);
    if (!file.open(QIODevice::ReadOnly))
        return;
    const qint64 size = file.size();
    if (size < followOffset_) {
        // 日志被轮转/截断：整页重读。
        reloadContent();
        return;
    }
    if (size == followOffset_)
        return;
    if (!file.seek(followOffset_)) {
        reloadContent();
        return;
    }
    const QByteArray added = file.read(size - followOffset_);
    followOffset_ = size;
    if (added.isEmpty())
        return;

    QByteArray buffer = pendingBytes_ + added;
    const int newline = buffer.lastIndexOf('\n');
    if (newline < 0) {
        pendingBytes_ = buffer;
        return;
    }
    pendingBytes_ = buffer.mid(newline + 1);
    buffer.truncate(newline);

    QVector<LogLine> lines;
    for (const QString &raw : QString::fromUtf8(buffer).split(QLatin1Char('\n'), Qt::SkipEmptyParts)) {
        const LogLine line = logutil::parseLine(raw);
        if (matchLevel(line) && matchKeyword(line))
            lines.append(line);
    }
    appendLines(lines);
    displayedLines_ += lines.size();
    updateCountLabel();
}

void LogPage::appendLines(const QVector<LogLine> &lines)
{
    if (lines.isEmpty())
        return;
    auto *bar = view_->verticalScrollBar();
    const bool atBottom = bar->value() >= bar->maximum() - 4;
    for (const LogLine &line : lines)
        view_->appendHtml(renderLine(line));
    if (atBottom)
        view_->moveCursor(QTextCursor::End);
}

QString LogPage::renderLine(const LogLine &line) const
{
    const QString keyword = keywordEdit_ ? keywordEdit_->text().trimmed() : QString();

    if (!line.parsed) {
        // 纯文本（ddns-go / cloudflared 这类子进程日志）保留原文，不再硬塞时间/级别列。
        return QStringLiteral("<span style=\"color:#7C8CA6\">%1</span>")
            .arg(highlightHtml(toFieldHtml(line.message), keyword));
    }

    const QString time = line.time.isEmpty() ? QStringLiteral("—") : line.time;
    const QString level = line.level.isEmpty() ? QStringLiteral("info") : line.level.toUpper();
    const QString caller = line.caller.isEmpty() ? QStringLiteral("-") : line.caller;
    const QString message = highlightHtml(toFieldHtml(line.message), keyword);

    return QStringLiteral("<span style=\"color:#64748B\">%1</span>%2"
                          "<span style=\"color:%3\">%4</span>%2"
                          "<span style=\"color:#64748B\">%5</span>%2"
                          "<span style=\"color:#D6DEE8\">%6</span>")
        .arg(toFieldHtml(padField(time, 18)),
             QStringLiteral("&nbsp;&nbsp;"),
             darkLevelColor(line.level),
             toFieldHtml(padField(level, 5)),
             toFieldHtml(padField(caller, 26)),
             message);
}

bool LogPage::matchLevel(const LogLine &line) const
{
    if (levelThreshold_ == 0 || !line.parsed)
        return true;
    return logutil::levelRank(line.level) >= levelThreshold_;
}

bool LogPage::matchKeyword(const LogLine &line) const
{
    const QString keyword = keywordEdit_ ? keywordEdit_->text().trimmed() : QString();
    if (keyword.isEmpty())
        return true;
    return line.raw.contains(keyword, Qt::CaseInsensitive);
}

void LogPage::updateCountLabel()
{
    countLabel_->setText(displayedLines_ > 0 ? QStringLiteral("已显示 %1 行").arg(displayedLines_) : QString());
}

void LogPage::openLogDir()
{
    const QString dir = logDir();
    if (!QDir(dir).exists()) {
        view_->clear();
        view_->appendHtml(QStringLiteral("<span style=\"color:#F87171\">日志目录不存在：%1</span>")
                              .arg(logutil::escapeHtml(dir)));
        return;
    }
    QDesktopServices::openUrl(QUrl::fromLocalFile(dir));
}