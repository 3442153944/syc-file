#pragma once

#include "appinfo.h"

#include <QMainWindow>
#include <QString>

class BackendClient;
class ConfigPage;
class LogPage;
class OverviewPage;
struct BackendStatus;
class QLabel;
class QMenu;
class QPushButton;
class QStackedWidget;
class QSystemTrayIcon;
class QTimer;

// MainWindow 只做「外壳」：侧边栏导航 + 顶栏（标题/状态/操作）+ 页面栈 + 系统托盘。
// 概览/配置/日志的业务逻辑分别在 OverviewPage / ConfigPage / LogPage 里。
class MainWindow : public QMainWindow {
    Q_OBJECT

public:
    explicit MainWindow(BackendClient *client, QWidget *parent = nullptr);

    // startPolling 启动 2s 状态轮询（构造函数之后由 main 调用，便于测试时不起定时器）。
    void startPolling();

    // showPage 切换页面（0 概览 / 1 配置 / 2 日志）；侧边栏与应用测试都走这里。
    void showPage(int index);

    // showWindow 从托盘/单实例唤醒窗口（其他实例发来 "show" 时调用）。
    void showWindow();

protected:
    void closeEvent(QCloseEvent *event) override;

private slots:
    void refreshStatus();
    void applyStatus(const BackendStatus &status);
    void onActionFinished(const QString &action, bool ok, const QString &message);

private:
    void buildSidebar();
    void buildHeader();
    void buildTray();
    void updateTrayIcon(const BackendStatus &status);
    void updateActions(const BackendStatus &status);
    void updateTrayTooltip();
    void selectPage(int index);

    BackendClient *client_ = nullptr;

    QStackedWidget *stack_ = nullptr;
    OverviewPage *overview_ = nullptr;
    ConfigPage *configPage_ = nullptr;
    LogPage *logPage_ = nullptr;

    QWidget *sidebar_ = nullptr;
    QWidget *header_ = nullptr;
    QPushButton *navOverview_ = nullptr;
    QPushButton *navConfig_ = nullptr;
    QPushButton *navLogs_ = nullptr;
    QLabel *sidebarState_ = nullptr;
    QLabel *sidebarVersion_ = nullptr;

    QLabel *title_ = nullptr;
    QLabel *subtitle_ = nullptr;
    QLabel *statusChip_ = nullptr;
    QPushButton *refreshButton_ = nullptr;
    QPushButton *startButton_ = nullptr;
    QPushButton *restartButton_ = nullptr;
    QPushButton *stopButton_ = nullptr;

    QSystemTrayIcon *tray_ = nullptr;
    QMenu *trayMenu_ = nullptr;
    QTimer *statusTimer_ = nullptr;
    QLabel *lastRefreshLabel_ = nullptr;

    QString trayTooltip_ = appinfo::displayName();
    bool quitting_ = false;
    bool informedHide_ = false;
    bool backendKnown_ = false;
    bool backendRunning_ = false;
};