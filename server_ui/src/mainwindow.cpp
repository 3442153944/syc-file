#include "mainwindow.h"

#include "backendclient.h"
#include "appinfo.h"
#include "configpage.h"
#include "icons.h"
#include "logpage.h"
#include "overviewpage.h"
#include "theme.h"
#include "widgets.h"

#include <QAction>
#include <QApplication>
#include <QButtonGroup>
#include <QCloseEvent>
#include <QFrame>
#include <QHBoxLayout>
#include <QIcon>
#include <QLabel>
#include <QMenu>
#include <QPushButton>
#include <QStackedWidget>
#include <QStatusBar>
#include <QStyle>
#include <QSystemTrayIcon>
#include <QTime>
#include <QTimer>
#include <QVBoxLayout>

namespace {

void repolish(QWidget *widget)
{
    widget->style()->unpolish(widget);
    widget->style()->polish(widget);
    widget->update();
}

} // namespace

MainWindow::MainWindow(BackendClient *client, QWidget *parent)
    : QMainWindow(parent), client_(client)
{
    setWindowTitle(appinfo::displayName());
    setWindowIcon(QIcon(icons::appIcon(256)));
    setMinimumSize(1080, 700);
    resize(1200, 780);

    overview_ = new OverviewPage(client_, this);
    configPage_ = new ConfigPage(client_, this);
    logPage_ = new LogPage(client_, this);

    stack_ = new QStackedWidget(this);
    stack_->addWidget(overview_);
    stack_->addWidget(configPage_);
    stack_->addWidget(logPage_);

    auto *root = new QWidget(this);
    root->setObjectName(QStringLiteral("AppRoot"));
    auto *rootLayout = new QHBoxLayout(root);
    rootLayout->setContentsMargins(0, 0, 0, 0);
    rootLayout->setSpacing(0);

    buildSidebar();

    auto *right = new QWidget(root);
    auto *rightLayout = new QVBoxLayout(right);
    rightLayout->setContentsMargins(0, 0, 0, 0);
    rightLayout->setSpacing(0);
    buildHeader();
    rightLayout->addWidget(header_);
    rightLayout->addWidget(stack_, 1);

    rootLayout->addWidget(sidebar_);
    rootLayout->addWidget(right, 1);
    setCentralWidget(root);

    buildTray();

    lastRefreshLabel_ = new QLabel(QStringLiteral("等待首次刷新"), this);
    lastRefreshLabel_->setObjectName(QStringLiteral("Hint"));
    statusBar()->addPermanentWidget(lastRefreshLabel_);
    statusBar()->showMessage(QStringLiteral("就绪"), 3000);

    connect(client_, &BackendClient::statusUpdated, this, &MainWindow::applyStatus);
    connect(client_, &BackendClient::actionFinished, this, &MainWindow::onActionFinished);
    connect(configPage_, &ConfigPage::saved, this, &MainWindow::refreshStatus);

    selectPage(0);
}

void MainWindow::buildSidebar()
{
    sidebar_ = new QWidget(this);
    sidebar_->setObjectName(QStringLiteral("Sidebar"));
    sidebar_->setFixedWidth(224);

    auto *layout = new QVBoxLayout(sidebar_);
    layout->setContentsMargins(16, 20, 16, 16);
    layout->setSpacing(6);

    auto *brandRow = new QHBoxLayout();
    brandRow->setSpacing(10);
    auto *logo = new QLabel(sidebar_);
    logo->setPixmap(icons::appIcon(36));
    logo->setFixedSize(36, 36);
    auto *brandTexts = new QVBoxLayout();
    brandTexts->setSpacing(0);
    auto *name = new QLabel(appinfo::displayName(), sidebar_);
    name->setObjectName(QStringLiteral("BrandTitle"));
    auto *sub = new QLabel(appinfo::subtitle(), sidebar_);
    sub->setObjectName(QStringLiteral("BrandSub"));
    brandTexts->addWidget(name);
    brandTexts->addWidget(sub);
    brandRow->addWidget(logo);
    brandRow->addLayout(brandTexts, 1);
    layout->addLayout(brandRow);
    layout->addSpacing(20);

    auto *navGroup = new QButtonGroup(this);
    navGroup->setExclusive(true);

    auto makeNav = [&](const QString &text, icons::Glyph glyph, int index) {
        auto *button = new QPushButton(icons::dual(glyph, 18), text, sidebar_);
        button->setObjectName(QStringLiteral("NavButton"));
        button->setCheckable(true);
        button->setIconSize(QSize(18, 18));
        button->setCursor(Qt::PointingHandCursor);
        button->setMinimumHeight(38);
        navGroup->addButton(button, index);
        layout->addWidget(button);
        return button;
    };
    navOverview_ = makeNav(QStringLiteral("概览"), icons::Glyph::Home, 0);
    navConfig_ = makeNav(QStringLiteral("配置"), icons::Glyph::Settings, 1);
    navLogs_ = makeNav(QStringLiteral("日志"), icons::Glyph::Document, 2);

    connect(navGroup, &QButtonGroup::idClicked, this, &MainWindow::selectPage);

    layout->addStretch(1);

    auto *line = new QFrame(sidebar_);
    line->setObjectName(QStringLiteral("SidebarLine"));
    line->setFixedHeight(1);
    layout->addWidget(line);
    layout->addSpacing(10);

    sidebarState_ = new QLabel(QStringLiteral("● 正在检测后端…"), sidebar_);
    sidebarState_->setObjectName(QStringLiteral("SidebarState"));
    sidebarState_->setStyleSheet(QStringLiteral("color:#FBBF24;"));
    sidebarVersion_ = new QLabel(QStringLiteral("本地管理接口未连接"), sidebar_);
    sidebarVersion_->setObjectName(QStringLiteral("SidebarFooter"));
    layout->addWidget(sidebarState_);
    layout->addWidget(sidebarVersion_);
}

void MainWindow::buildHeader()
{
    header_ = new QWidget(this);
    header_->setObjectName(QStringLiteral("Header"));
    header_->setFixedHeight(86);

    auto *layout = new QHBoxLayout(header_);
    layout->setContentsMargins(24, 14, 24, 14);
    layout->setSpacing(10);

    auto *texts = new QVBoxLayout();
    texts->setSpacing(3);
    title_ = new QLabel(QStringLiteral("概览"), header_);
    title_->setObjectName(QStringLiteral("PageTitle"));
    subtitle_ = new QLabel(QStringLiteral("后端运行状态、依赖与路径"), header_);
    subtitle_->setObjectName(QStringLiteral("PageSubtitle"));
    texts->addWidget(title_);
    texts->addWidget(subtitle_);
    layout->addLayout(texts, 1);

    statusChip_ = new QLabel(QStringLiteral("● 检测中"), header_);
    statusChip_->setObjectName(QStringLiteral("StatusChip"));
    statusChip_->setProperty("state", QStringLiteral("warning"));
    statusChip_->setFixedHeight(30);
    layout->addWidget(statusChip_, 0, Qt::AlignVCenter);
    layout->addSpacing(4);

    refreshButton_ = new QPushButton(header_);
    refreshButton_->setObjectName(QStringLiteral("GhostBtn"));
    refreshButton_->setIcon(icons::make(icons::Glyph::Refresh, theme::Color::TextSecondary, 17));
    refreshButton_->setIconSize(QSize(17, 17));
    refreshButton_->setFixedSize(34, 34);
    refreshButton_->setToolTip(QStringLiteral("立即刷新状态"));
    refreshButton_->setCursor(Qt::PointingHandCursor);

    startButton_ = new QPushButton(icons::make(icons::Glyph::Play, Qt::white, 16), QStringLiteral("启动后端"), header_);
    startButton_->setObjectName(QStringLiteral("Primary"));
    restartButton_ = new QPushButton(icons::make(icons::Glyph::Restart, theme::Color::TextSecondary, 16),
                                     QStringLiteral("重启后端"), header_);
    stopButton_ = new QPushButton(icons::make(icons::Glyph::Stop, theme::Color::Danger, 16),
                                  QStringLiteral("停止后端"), header_);
    stopButton_->setObjectName(QStringLiteral("DangerBtn"));
    for (QPushButton *button : {startButton_, restartButton_, stopButton_})
        button->setCursor(Qt::PointingHandCursor);

    layout->addWidget(refreshButton_);
    layout->addSpacing(4);
    layout->addWidget(startButton_);
    layout->addWidget(restartButton_);
    layout->addWidget(stopButton_);

    connect(refreshButton_, &QPushButton::clicked, this, &MainWindow::refreshStatus);
    connect(startButton_, &QPushButton::clicked, client_, &BackendClient::startBackend);
    connect(restartButton_, &QPushButton::clicked, client_, &BackendClient::restartBackend);
    connect(stopButton_, &QPushButton::clicked, client_, &BackendClient::stopBackend);
}

void MainWindow::buildTray()
{
    if (!QSystemTrayIcon::isSystemTrayAvailable()) {
        statusBar()->showMessage(QStringLiteral("系统托盘不可用：关闭窗口将直接退出"), 8000);
        return;
    }

    trayMenu_ = new QMenu(this);
    auto *openAction = trayMenu_->addAction(QStringLiteral("打开控制台"));
    connect(openAction, &QAction::triggered, this, &MainWindow::showWindow);

    auto *configAction = trayMenu_->addAction(QStringLiteral("打开配置文件"));
    connect(configAction, &QAction::triggered, configPage_, &ConfigPage::openFile);
    auto *logAction = trayMenu_->addAction(QStringLiteral("打开日志目录"));
    connect(logAction, &QAction::triggered, logPage_, &LogPage::openLogDir);

    trayMenu_->addSeparator();
    auto *startAction = trayMenu_->addAction(QStringLiteral("启动后端"));
    connect(startAction, &QAction::triggered, client_, &BackendClient::startBackend);
    auto *restartAction = trayMenu_->addAction(QStringLiteral("重启后端"));
    connect(restartAction, &QAction::triggered, client_, &BackendClient::restartBackend);
    auto *stopAction = trayMenu_->addAction(QStringLiteral("停止后端"));
    connect(stopAction, &QAction::triggered, client_, &BackendClient::stopBackend);

    trayMenu_->addSeparator();
    auto *quitAction = trayMenu_->addAction(QStringLiteral("退出控制台"));
    connect(quitAction, &QAction::triggered, this, [this]() {
        quitting_ = true;
        if (tray_)
            tray_->hide();
        qApp->quit();
    });

    tray_ = new QSystemTrayIcon(QIcon(icons::appIcon(64, true, false, false)), this);
    tray_->setContextMenu(trayMenu_);
    tray_->setToolTip(trayTooltip_);
    connect(tray_, &QSystemTrayIcon::activated, this,
            [this](QSystemTrayIcon::ActivationReason reason) {
                if (reason == QSystemTrayIcon::Trigger || reason == QSystemTrayIcon::DoubleClick)
                    showWindow();
            });
    tray_->show();
}

void MainWindow::startPolling()
{
    statusTimer_ = new QTimer(this);
    statusTimer_->setInterval(2000);
    connect(statusTimer_, &QTimer::timeout, this, &MainWindow::refreshStatus);
    statusTimer_->start();
    refreshStatus();
}

void MainWindow::refreshStatus()
{
    lastRefreshLabel_->setText(QStringLiteral("刷新中…"));
    client_->fetchStatus();
}

void MainWindow::applyStatus(const BackendStatus &status)
{
    backendRunning_ = status.valid;
    backendKnown_ = status.valid || status.runtimePresent;

    overview_->applyStatus(status);
    logPage_->setStatus(status);
    updateActions(status);
    updateTrayIcon(status);

    if (status.valid) {
        statusChip_->setText(QStringLiteral("● 运行中"));
        statusChip_->setProperty("state", QStringLiteral("running"));
        sidebarState_->setText(QStringLiteral("● 后端运行中"));
        sidebarState_->setStyleSheet(QStringLiteral("color:#34D399;"));
        sidebarVersion_->setText(QStringLiteral("后端 v%1 · 端口 %2").arg(status.version).arg(status.port));
        trayTooltip_ = appinfo::displayName()
                       + QStringLiteral("\n运行中 · pid %1 · 端口 %2").arg(status.pid).arg(status.port);
    } else if (status.runtimePresent) {
        statusChip_->setText(QStringLiteral("● 接口无响应"));
        statusChip_->setProperty("state", QStringLiteral("warning"));
        sidebarState_->setText(QStringLiteral("● 接口无响应"));
        sidebarState_->setStyleSheet(QStringLiteral("color:#FBBF24;"));
        sidebarVersion_->setText(QStringLiteral("本地管理接口未连接"));
        trayTooltip_ = appinfo::displayName() + QStringLiteral("\n本地管理接口无响应");
    } else {
        statusChip_->setText(QStringLiteral("● 未运行"));
        statusChip_->setProperty("state", QStringLiteral("stopped"));
        sidebarState_->setText(QStringLiteral("● 后端未运行"));
        sidebarState_->setStyleSheet(QStringLiteral("color:#F87171;"));
        sidebarVersion_->setText(QStringLiteral("本地管理接口未连接"));
        trayTooltip_ = appinfo::displayName() + QStringLiteral("\n未运行");
    }
    repolish(statusChip_);
    updateTrayTooltip();

    lastRefreshLabel_->setText(QStringLiteral("刷新于 %1").arg(QTime::currentTime().toString(QStringLiteral("HH:mm:ss"))));
    if (status.valid) {
        const QString message = QStringLiteral("后端正常：pid %1 · 端口 %2 · 数据库%3 · Redis%4")
                                    .arg(status.pid)
                                    .arg(status.port)
                                    .arg(status.dbOK ? QStringLiteral("正常") : QStringLiteral("异常"),
                                         status.redisOK ? QStringLiteral("正常") : QStringLiteral("异常"));
        statusBar()->showMessage(message, 2500);
    }
}

void MainWindow::updateActions(const BackendStatus &status)
{
    startButton_->setEnabled(!status.valid);
    restartButton_->setEnabled(status.valid);
    stopButton_->setEnabled(status.valid);
}

void MainWindow::updateTrayIcon(const BackendStatus &status)
{
    if (!tray_)
        return;
    tray_->setIcon(QIcon(icons::appIcon(64, true, status.valid, backendKnown_)));
}

void MainWindow::updateTrayTooltip()
{
    if (tray_)
        tray_->setToolTip(trayTooltip_);
}

void MainWindow::onActionFinished(const QString &action, bool ok, const QString &message)
{
    Q_UNUSED(action);
    statusBar()->showMessage(message, ok ? 5000 : 10000);
    statusBar()->setStyleSheet(ok ? QString() : QStringLiteral("QStatusBar{color:#B91C1C;}"));
    refreshStatus();
}

void MainWindow::selectPage(int index)
{
    if (index < 0 || index > 2 || !stack_)
        return;
    stack_->setCurrentIndex(index);

    static const char *titles[] = {"概览", "配置", "日志"};
    static const char *subtitles[] = {
        "后端运行状态、依赖与路径",
        "修改 config.yaml，热字段保存后即时生效",
        "按级别过滤、关键词搜索、实时跟随",
    };
    title_->setText(QString::fromUtf8(titles[index]));
    subtitle_->setText(QString::fromUtf8(subtitles[index]));
    navOverview_->setChecked(index == 0);
    navConfig_->setChecked(index == 1);
    navLogs_->setChecked(index == 2);

    if (index == 1)
        configPage_->reload();
    else if (index == 2)
        logPage_->reloadFiles();
}

void MainWindow::showPage(int index)
{
    selectPage(index);
}

void MainWindow::showWindow()
{
    showNormal();
    raise();
    activateWindow();
    refreshStatus();
}

void MainWindow::closeEvent(QCloseEvent *event)
{
    if (!quitting_ && tray_ && tray_->isVisible()) {
        hide();
        event->ignore();
        if (!informedHide_) {
            tray_->showMessage(appinfo::displayName(),
                               QStringLiteral("窗口已隐藏到托盘，双击托盘图标可重新打开。"),
                               QSystemTrayIcon::Information, 3000);
            informedHide_ = true;
        }
        return;
    }
    event->accept();
}