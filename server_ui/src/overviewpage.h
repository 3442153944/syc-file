#pragma once

#include <QWidget>

class BackendClient;
struct BackendStatus;
struct SupervisorInfo;
class QGridLayout;
class QLabel;
class QVBoxLayout;
class QHBoxLayout;

namespace ui {
class Card;
class Pill;
class StatCard;
class StatusOrb;
} // namespace ui

// OverviewPage 是「运行状态仪表盘」：
//   状态大卡（自绘指示灯 + 一句话 + 刷新时间）
//   四张指标卡（运行时长 / 端口 / 内存 / 依赖）
//   运行信息 + 依赖与托管进程 + 路径信息
// 页面自己不发起请求，只消费 BackendClient::statusUpdated。
class OverviewPage : public QWidget {
    Q_OBJECT

public:
    explicit OverviewPage(BackendClient *client, QWidget *parent = nullptr);

    // applyStatus 用一次快照刷新整页；status.valid=false 时按「进程活着但接口不通」/「没运行」区分展示。
    void applyStatus(const BackendStatus &status);

private:
    QLabel *addInfo(QGridLayout *grid, int row, int col, const QString &key);
    void addPathRow(QVBoxLayout *layout, const QString &key, QLabel **valueOut, bool openAsDirectory);
    void rebuildSupervisor(const QVector<SupervisorInfo> &list);
    void setOffline(const BackendStatus &status);

    BackendClient *client_ = nullptr;

    ui::StatusOrb *orb_ = nullptr;
    QLabel *heroTitle_ = nullptr;
    QLabel *heroSub_ = nullptr;
    QLabel *refreshLabel_ = nullptr;

    ui::StatCard *uptimeCard_ = nullptr;
    ui::StatCard *portCard_ = nullptr;
    ui::StatCard *memoryCard_ = nullptr;
    ui::StatCard *depsCard_ = nullptr;

    QLabel *version_ = nullptr;
    QLabel *mode_ = nullptr;
    QLabel *node_ = nullptr;
    QLabel *pid_ = nullptr;
    QLabel *started_ = nullptr;
    QLabel *port_ = nullptr;
    QLabel *ws_ = nullptr;
    QLabel *runtime_ = nullptr;

    ui::Pill *dbPill_ = nullptr;
    ui::Pill *redisPill_ = nullptr;
    QLabel *memoryText_ = nullptr;
    QLabel *goroutineText_ = nullptr;
    QGridLayout *supervisorGrid_ = nullptr;
    QString supervisorSignature_;

    QLabel *configPath_ = nullptr;
    QLabel *logDir_ = nullptr;
    QLabel *workDir_ = nullptr;
};