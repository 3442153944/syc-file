#include "overviewpage.h"

#include "backendclient.h"
#include "icons.h"
#include "logline.h"
#include "theme.h"
#include "widgets.h"

#include <QClipboard>
#include <QDesktopServices>
#include <QGridLayout>
#include <QGuiApplication>
#include <QHBoxLayout>
#include <QLabel>
#include <QPointer>
#include <QPushButton>
#include <QScrollArea>
#include <QTime>
#include <QTimer>
#include <QUrl>
#include <QVBoxLayout>

OverviewPage::OverviewPage(BackendClient *client, QWidget *parent)
    : QWidget(parent), client_(client)
{
    setObjectName(QStringLiteral("Page"));

    auto *content = new QWidget;
    auto *outer = new QVBoxLayout(content);
    outer->setContentsMargins(24, 20, 24, 24);
    outer->setSpacing(16);

    // ---- 状态大卡 ----
    auto *hero = new ui::Card(this, ui::Card::Kind::Hero);
    auto *heroRow = new QHBoxLayout();
    heroRow->setContentsMargins(0, 2, 0, 2);
    heroRow->setSpacing(16);

    orb_ = new ui::StatusOrb(hero);
    auto *heroTexts = new QVBoxLayout();
    heroTexts->setContentsMargins(0, 0, 0, 0);
    heroTexts->setSpacing(4);
    heroTitle_ = new QLabel(QStringLiteral("正在检测后端…"), hero);
    heroTitle_->setObjectName(QStringLiteral("HeroTitle"));
    heroSub_ = new QLabel(QStringLiteral("读取 runtime.json 并连接本地管理接口"), hero);
    heroSub_->setObjectName(QStringLiteral("HeroSub"));
    heroSub_->setWordWrap(true);
    heroTexts->addWidget(heroTitle_);
    heroTexts->addWidget(heroSub_);

    auto *heroRight = new QVBoxLayout();
    heroRight->setContentsMargins(0, 0, 0, 0);
    heroRight->setSpacing(2);
    heroRight->setAlignment(Qt::AlignRight | Qt::AlignVCenter);
    refreshLabel_ = new QLabel(QStringLiteral("尚未刷新"), hero);
    refreshLabel_->setObjectName(QStringLiteral("Muted"));
    refreshLabel_->setAlignment(Qt::AlignRight);
    auto *autoHint = new QLabel(QStringLiteral("每 2 秒自动刷新"), hero);
    autoHint->setObjectName(QStringLiteral("Hint"));
    autoHint->setAlignment(Qt::AlignRight);
    heroRight->addWidget(refreshLabel_);
    heroRight->addWidget(autoHint);

    heroRow->addWidget(orb_, 0, Qt::AlignTop);
    heroRow->addLayout(heroTexts, 1);
    heroRow->addLayout(heroRight);
    hero->addBodyLayout(heroRow);
    outer->addWidget(hero);

    // ---- 指标卡 ----
    auto *stats = new QHBoxLayout();
    stats->setSpacing(16);
    uptimeCard_ = new ui::StatCard(QStringLiteral("已运行"), theme::Color::Accent, this);
    portCard_ = new ui::StatCard(QStringLiteral("HTTP 端口"), QColor("#0EA5E9"), this);
    memoryCard_ = new ui::StatCard(QStringLiteral("内存占用"), QColor("#7C3AED"), this);
    depsCard_ = new ui::StatCard(QStringLiteral("依赖状态"), theme::Color::Success, this);
    for (ui::StatCard *card : {uptimeCard_, portCard_, memoryCard_, depsCard_})
        stats->addWidget(card, 1);
    outer->addLayout(stats);

    // ---- 运行信息 / 依赖与资源 ----
    auto *mid = new QHBoxLayout();
    mid->setSpacing(16);

    auto *infoCard = new ui::Card(this);
    infoCard->setTitle(QStringLiteral("运行信息"), QStringLiteral("来自后端本地接口"));
    auto *infoGrid = new QGridLayout();
    infoGrid->setHorizontalSpacing(28);
    infoGrid->setVerticalSpacing(14);
    int row = 0;
    version_ = addInfo(infoGrid, row, 0, QStringLiteral("版本"));
    mode_ = addInfo(infoGrid, row, 1, QStringLiteral("运行模式"));
    ++row;
    node_ = addInfo(infoGrid, row, 0, QStringLiteral("节点名称"));
    pid_ = addInfo(infoGrid, row, 1, QStringLiteral("进程 PID"));
    ++row;
    started_ = addInfo(infoGrid, row, 0, QStringLiteral("启动时间"));
    port_ = addInfo(infoGrid, row, 1, QStringLiteral("HTTP 端口"));
    ++row;
    ws_ = addInfo(infoGrid, row, 0, QStringLiteral("WebSocket"));
    runtime_ = addInfo(infoGrid, row, 1, QStringLiteral("运行时"));
    infoGrid->setColumnStretch(1, 1);
    infoGrid->setColumnStretch(3, 1);
    // 最后补一行弹簧：卡片被拉高时让字段贴顶排列，而不是均分到各行。
    infoGrid->setRowStretch(row + 1, 1);
    infoCard->addBodyLayout(infoGrid);

    auto *depsCard = new ui::Card(this);
    depsCard->setTitle(QStringLiteral("依赖与资源"), QStringLiteral("数据库 / Redis / 托管进程"));
    auto *depsGrid = new QGridLayout();
    depsGrid->setHorizontalSpacing(20);
    depsGrid->setVerticalSpacing(12);
    dbPill_ = new ui::Pill(QStringLiteral("未知"), ui::Pill::Neutral, depsCard);
    redisPill_ = new ui::Pill(QStringLiteral("未知"), ui::Pill::Neutral, depsCard);
    auto *dbKey = new QLabel(QStringLiteral("数据库"), depsCard);
    dbKey->setObjectName(QStringLiteral("Key"));
    auto *redisKey = new QLabel(QStringLiteral("Redis"), depsCard);
    redisKey->setObjectName(QStringLiteral("Key"));
    memoryText_ = new QLabel(QStringLiteral("—"), depsCard);
    memoryText_->setObjectName(QStringLiteral("Value"));
    goroutineText_ = new QLabel(QStringLiteral("—"), depsCard);
    goroutineText_->setObjectName(QStringLiteral("Value"));
    auto *memoryKey = new QLabel(QStringLiteral("内存占用"), depsCard);
    memoryKey->setObjectName(QStringLiteral("Key"));
    auto *goroutineKey = new QLabel(QStringLiteral("Goroutine"), depsCard);
    goroutineKey->setObjectName(QStringLiteral("Key"));
    depsGrid->addWidget(dbKey, 0, 0);
    depsGrid->addWidget(dbPill_, 0, 1, Qt::AlignLeft);
    depsGrid->addWidget(redisKey, 0, 2);
    depsGrid->addWidget(redisPill_, 0, 3, Qt::AlignLeft);
    depsGrid->addWidget(memoryKey, 1, 0);
    depsGrid->addWidget(memoryText_, 1, 1);
    depsGrid->addWidget(goroutineKey, 1, 2);
    depsGrid->addWidget(goroutineText_, 1, 3);
    depsGrid->setColumnStretch(1, 1);
    depsGrid->setColumnStretch(3, 1);
    depsCard->addBodyLayout(depsGrid);
    depsCard->addBodyWidget(ui::sectionLine(depsCard));

    auto *managedKey = new QLabel(QStringLiteral("托管进程"), depsCard);
    managedKey->setObjectName(QStringLiteral("Key"));
    depsCard->addBodyWidget(managedKey);
    supervisorGrid_ = new QGridLayout();
    supervisorGrid_->setContentsMargins(0, 0, 0, 0);
    supervisorGrid_->setHorizontalSpacing(12);
    supervisorGrid_->setVerticalSpacing(6);
    depsCard->addBodyLayout(supervisorGrid_);

    mid->addWidget(infoCard, 3);
    mid->addWidget(depsCard, 2);
    outer->addLayout(mid);

    // ---- 路径信息 ----
    auto *pathCard = new ui::Card(this);
    pathCard->setTitle(QStringLiteral("路径信息"), QStringLiteral("配置文件 / 日志 / 工作目录"));
    addPathRow(pathCard->body(), QStringLiteral("配置文件"), &configPath_, false);
    addPathRow(pathCard->body(), QStringLiteral("日志目录"), &logDir_, true);
    addPathRow(pathCard->body(), QStringLiteral("工作目录"), &workDir_, true);
    outer->addWidget(pathCard);

    auto *hint = new QLabel(
        QStringLiteral("提示：后端 watch config.yaml，标注「即时生效」的字段保存后立即生效；"
                       "「需重启」的字段要等右上角「重启后端」；概览每 2 秒自动刷新。"),
        this);
    hint->setObjectName(QStringLiteral("Hint"));
    hint->setWordWrap(true);
    outer->addWidget(hint);
    outer->addStretch(1);

    auto *pageLayout = new QVBoxLayout(this);
    pageLayout->setContentsMargins(0, 0, 0, 0);
    pageLayout->addWidget(ui::scrollPage(content, this));

    // 初始占位：路径类信息不依赖后端，先展示出来。
    configPath_->setText(client_->configPath());
    logDir_->setText(client_->logDir(BackendStatus{}));
    workDir_->setText(client_->backendWorkDir());
}

QLabel *OverviewPage::addInfo(QGridLayout *grid, int row, int col, const QString &key)
{
    auto *keyLabel = new QLabel(key, grid->parentWidget());
    keyLabel->setObjectName(QStringLiteral("Key"));
    auto *value = new QLabel(QStringLiteral("—"), grid->parentWidget());
    value->setObjectName(QStringLiteral("Value"));
    value->setTextInteractionFlags(Qt::TextSelectableByMouse);
    grid->addWidget(keyLabel, row, col * 2);
    grid->addWidget(value, row, col * 2 + 1);
    return value;
}

void OverviewPage::addPathRow(QVBoxLayout *layout, const QString &key, QLabel **valueOut, bool openAsDirectory)
{
    auto *row = new QWidget(layout->parentWidget());
    auto *rowLayout = new QHBoxLayout(row);
    rowLayout->setContentsMargins(0, 0, 0, 0);
    rowLayout->setSpacing(8);

    auto *keyLabel = new QLabel(key, row);
    keyLabel->setObjectName(QStringLiteral("Key"));
    keyLabel->setFixedWidth(76);
    auto *value = new QLabel(QStringLiteral("—"), row);
    value->setObjectName(QStringLiteral("PathValue"));
    value->setTextInteractionFlags(Qt::TextSelectableByMouse);
    value->setWordWrap(true);
    value->setSizePolicy(QSizePolicy::Expanding, QSizePolicy::Preferred);
    *valueOut = value;

    auto *copyButton = ui::iconButton(icons::make(icons::Glyph::Copy, theme::Color::TextMuted), QStringLiteral("复制路径"), row);
    auto *openButton = ui::iconButton(
        icons::make(openAsDirectory ? icons::Glyph::Folder : icons::Glyph::OpenFile, theme::Color::TextMuted),
        openAsDirectory ? QStringLiteral("打开目录") : QStringLiteral("打开文件"), row);

    QObject::connect(copyButton, &QPushButton::clicked, row, [copyButton, value]() {
        QGuiApplication::clipboard()->setText(value->text());
        const QString oldTooltip = copyButton->toolTip();
        copyButton->setToolTip(QStringLiteral("已复制"));
        QPointer<QPushButton> guard(copyButton);
        QTimer::singleShot(1400, copyButton, [guard, oldTooltip]() {
            if (guard)
                guard->setToolTip(oldTooltip);
        });
    });
    QObject::connect(openButton, &QPushButton::clicked, row, [value]() {
        if (!value->text().isEmpty())
            QDesktopServices::openUrl(QUrl::fromLocalFile(value->text()));
    });

    rowLayout->addWidget(keyLabel);
    rowLayout->addWidget(value, 1);
    rowLayout->addWidget(copyButton, 0, Qt::AlignTop);
    rowLayout->addWidget(openButton, 0, Qt::AlignTop);
    layout->addWidget(row);
}

void OverviewPage::applyStatus(const BackendStatus &status)
{
    refreshLabel_->setText(QStringLiteral("最近刷新 %1").arg(QTime::currentTime().toString(QStringLiteral("HH:mm:ss"))));

    if (!status.valid) {
        setOffline(status);
        return;
    }

    orb_->setState(true);
    heroTitle_->setText(QStringLiteral("后端运行中"));
    heroSub_->setText(QStringLiteral("HTTP %1 · PID %2 · 节点 %3")
                          .arg(status.port)
                          .arg(status.pid)
                          .arg(status.nodeName.isEmpty() ? QStringLiteral("—") : status.nodeName));

    uptimeCard_->setValue(logutil::formatUptime(status.uptimeNs));
    uptimeCard_->setSub(QStringLiteral("启动于 %1").arg(logutil::formatTime(status.startedAt)));
    portCard_->setValue(QString::number(status.port));
    portCard_->setSub(QStringLiteral("%1 个 WebSocket 连接").arg(status.onlineConns));
    memoryCard_->setValue(logutil::formatBytes(status.memoryBytes));
    memoryCard_->setSub(QStringLiteral("goroutine %1").arg(status.goroutines));
    const bool depsOK = status.dbOK && status.redisOK;
    depsCard_->setValue(depsOK ? QStringLiteral("正常") : QStringLiteral("异常"));
    depsCard_->setSub(depsOK ? QStringLiteral("数据库 / Redis 均已连接")
                             : QStringLiteral("数据库 / Redis 有一项不可用"));

    version_->setText(status.version.isEmpty() ? QStringLiteral("—") : status.version);
    mode_->setText(status.mode.isEmpty() ? QStringLiteral("—") : status.mode);
    node_->setText(status.nodeName.isEmpty() ? QStringLiteral("—") : status.nodeName);
    pid_->setText(QString::number(status.pid));
    started_->setText(logutil::formatTime(status.startedAt));
    port_->setText(QString::number(status.port));
    ws_->setText(QStringLiteral("%1 个连接").arg(status.onlineConns));
    runtime_->setText(QStringLiteral("%1 / %2 / %3")
                          .arg(status.platform.isEmpty() ? QStringLiteral("—") : status.platform,
                               status.arch.isEmpty() ? QStringLiteral("—") : status.arch,
                               status.goVersion.isEmpty() ? QStringLiteral("—") : status.goVersion));

    dbPill_->setText(status.dbOK ? QStringLiteral("正常") : QStringLiteral("不可用"));
    dbPill_->setTone(status.dbOK ? ui::Pill::Success : ui::Pill::Danger);
    redisPill_->setText(status.redisOK ? QStringLiteral("正常") : QStringLiteral("不可用"));
    redisPill_->setTone(status.redisOK ? ui::Pill::Success : ui::Pill::Danger);
    memoryText_->setText(logutil::formatBytes(status.memoryBytes));
    goroutineText_->setText(QString::number(status.goroutines));
    rebuildSupervisor(status.supervisor);

    configPath_->setText(status.configPath.isEmpty() ? client_->configPath() : status.configPath);
    logDir_->setText(status.logDir.isEmpty() ? client_->logDir(status) : status.logDir);
    workDir_->setText(status.workingDir.isEmpty() ? client_->backendWorkDir() : status.workingDir);
}

void OverviewPage::setOffline(const BackendStatus &status)
{
    const bool warning = status.runtimePresent;
    orb_->setState(false, warning);
    heroTitle_->setText(warning ? QStringLiteral("本地接口没有响应")
                                : QStringLiteral("后端未运行"));
    QString detail = status.detail;
    if (detail.isEmpty())
        detail = QStringLiteral("可点右上角「启动后端」，或检查 Windows 服务 FileSyncBackend。");
    heroSub_->setText(detail);

    for (ui::StatCard *card : {uptimeCard_, portCard_, memoryCard_, depsCard_}) {
        card->setValue(QStringLiteral("—"));
        card->setSub(warning ? QStringLiteral("本地接口暂时不可用") : QStringLiteral("后端没有运行"));
    }
    for (QLabel *label : {version_, mode_, node_, pid_, started_, port_, ws_, runtime_})
        label->setText(QStringLiteral("—"));
    dbPill_->setText(QStringLiteral("未知"));
    dbPill_->setTone(ui::Pill::Neutral);
    redisPill_->setText(QStringLiteral("未知"));
    redisPill_->setTone(ui::Pill::Neutral);
    memoryText_->setText(QStringLiteral("—"));
    goroutineText_->setText(QStringLiteral("—"));
    rebuildSupervisor({});

    configPath_->setText(client_->configPath());
    logDir_->setText(client_->logDir(status));
    workDir_->setText(client_->backendWorkDir());
}

void OverviewPage::rebuildSupervisor(const QVector<SupervisorInfo> &list)
{
    QString signature;
    for (const SupervisorInfo &process : list) {
        signature += QStringLiteral("%1|%2|%3|%4;")
                         .arg(process.name)
                         .arg(process.running ? 1 : 0)
                         .arg(process.pid)
                         .arg(process.restarts);
    }
    if (signature == supervisorSignature_)
        return;
    supervisorSignature_ = signature;

    while (QLayoutItem *item = supervisorGrid_->takeAt(0)) {
        if (QWidget *widget = item->widget()) {
            widget->hide();
            widget->deleteLater();
        }
        delete item;
    }

    if (list.isEmpty()) {
        auto *empty = new QLabel(QStringLiteral("未托管外部进程"));
        empty->setObjectName(QStringLiteral("Hint"));
        supervisorGrid_->addWidget(empty, 0, 0, 1, 2);
        return;
    }

    int index = 0;
    for (const SupervisorInfo &process : list) {
        auto *row = new QWidget();
        auto *rowLayout = new QHBoxLayout(row);
        rowLayout->setContentsMargins(0, 0, 0, 0);
        rowLayout->setSpacing(6);

        auto *dot = new QLabel(QStringLiteral("●"), row);
        dot->setStyleSheet(QStringLiteral("color:%1;font-size:9px;")
                               .arg(process.running ? theme::Color::Success.name()
                                                    : theme::Color::Danger.name()));
        auto *name = new QLabel(process.name, row);
        name->setObjectName(QStringLiteral("Muted"));
        auto *meta = new QLabel(process.running ? QStringLiteral("pid %1").arg(process.pid)
                                                : QStringLiteral("未运行"),
                                row);
        meta->setObjectName(QStringLiteral("Hint"));
        rowLayout->addWidget(dot);
        rowLayout->addWidget(name);
        rowLayout->addWidget(meta);
        rowLayout->addStretch(1);
        if (process.restarts > 0) {
            auto *restarts = new ui::Pill(QStringLiteral("重启 %1").arg(process.restarts),
                                          ui::Pill::Warning, row);
            rowLayout->addWidget(restarts);
        }
        if (!process.lastError.isEmpty())
            row->setToolTip(process.lastError);
        supervisorGrid_->addWidget(row, index / 2, index % 2);
        ++index;
    }
    supervisorGrid_->setColumnStretch(0, 1);
    supervisorGrid_->setColumnStretch(1, 1);
}