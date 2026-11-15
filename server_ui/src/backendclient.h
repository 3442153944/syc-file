#pragma once

#include "ipcclient.h"

#include <QDateTime>
#include <QJsonObject>
#include <QMap>
#include <QObject>
#include <QString>
#include <QStringList>
#include <QVector>

#include <functional>

// SupervisorInfo 是后端托管的外部进程（frpc / ddns-go / cloudflared…）状态。
struct SupervisorInfo {
    QString name;
    bool running = false;
    int pid = 0;
    int restarts = 0;
    QString lastError;
};

// BackendStatus 是后端 localapi 的 /status 快照（字段与 internal/desktopapi.Status 对齐）。
struct BackendStatus {
    bool valid = false;         // 是否真的从后端拿到了数据（false = 离线/占位）
    bool runtimePresent = false; // runtime.json 是否存在（区分「没启动」和「进程在但接口不通」）
    QString detail;             // 离线/异常时的可读原因，直接展示给用户
    QString version;
    QString mode;
    QString nodeName;
    QString platform;
    QString arch;
    QString goVersion;
    int pid = 0;
    int port = 0;
    QDateTime startedAt;
    qint64 uptimeNs = 0;
    bool running = false;
    QString configPath;
    QString logDir;
    QString workingDir;
    quint64 memoryBytes = 0;
    int goroutines = 0;
    int onlineConns = 0;
    bool dbOK = false;
    bool redisOK = false;
    QVector<SupervisorInfo> supervisor;
};

// RuntimeInfo 对应后端写在本机 config 目录下的 runtime.json（{pid, ipc, token,...}）。
// IPC 在 Windows 上是命名管道名，在 Linux 上是 Unix socket 绝对路径。
struct RuntimeInfo {
    bool valid = false;
    int pid = 0;
    QString ipc;
    QString token;
    QString version;
    QString mode;
    QString configPath;
    QString workingDir;
    QDateTime startedAt;
};

// ConfigFieldInfo 是一个配置表单字段（后端 GET /config/fields 返回的元素）。
struct ConfigFieldInfo {
    QString path;
    QString label;
    QString group;
    QString kind; // string/int/bool/select/list/text/secret
    QString value;
    QString description;
    QStringList options;
    bool hotReload = false;
    bool readOnly = false;
};

// ConfigForm 是配置表单的完整数据。
struct ConfigForm {
    bool valid = false;
    QString path;
    QVector<ConfigFieldInfo> fields;
};

// BackendClient 负责「桌面控制台 ↔ 后端」的全部通信：
//   - 读 config.yaml 里 gui.* 配置（后端路径、工作目录、启动时最小化）；
//   - 读 runtime.json 拿到本机 IPC 端点与 token，轮询 /status；
//   - 配置表单：GET /config/fields、POST /config/fields（保存字段）；
//   - 原文保存：POST /config/validate；
//   - 后端没在运行时以脱离式进程拉起（GUI 退出不会带走后端）。
//
// 通信只走本机 IPC（命名管道 / Unix socket），不走 TCP：
// 代理的 TUN/透明接管可能劫持发往 127.0.0.1 的流量，IPC 不经过 IP 栈，无法被接管。
class BackendClient : public QObject {
    Q_OBJECT

public:
    explicit BackendClient(const QString &configPath, QObject *parent = nullptr);

    QString configPath() const { return configPath_; }
    QString configDir() const;
    QString configText(QString *error = nullptr) const;

    // gui 段配置（缺省值都合理，配置缺失也不影响启动）。
    bool startMinimized() const;
    QString backendExePath() const;
    QString backendWorkDir() const;
    // 日志目录：在线时用 /status 的 log_dir；离线时按 config log.path 推算。
    QString logDir(const BackendStatus &status) const;

    RuntimeInfo readRuntime() const;
    QString runtimePath() const;
    // backendRunning 依据最近一次 /status 轮询结果判断；还没轮询过时用 IPC 探测兜底。
    bool backendRunning() const;

    // 异步操作，结果通过信号回来。
    void fetchStatus();
    void startBackend();
    void stopBackend();    // 先 /shutdown，再兜底 kill
    void restartBackend(); // /shutdown → 等退出 → startBackend
    void validateConfig(const QString &text);
    void fetchConfigFields();
    // saveConfigFields 只提交「改动过的字段」：点分路径 → 文本值。
    void saveConfigFields(const QMap<QString, QString> &values);

signals:
    void statusUpdated(const BackendStatus &status);
    // action: start/stop/restart/validate；ok + 可直接展示的消息。
    void actionFinished(const QString &action, bool ok, const QString &message);
    void validateFinished(bool ok, const QString &message);
    void configFieldsLoaded(bool ok, const ConfigForm &form, const QString &error);
    void configSaved(bool ok, const QString &backup, const QStringList &hotApplied,
                     const QStringList &needRestart, const QString &error);

private:
    void post(const QString &path, const QByteArray &body, const QString &action,
              std::function<void(bool, const QString &)> done);
    void getStatus(std::function<void(bool, const BackendStatus &)> done);
    QString scalarValue(const QString &section, const QString &key, bool *found = nullptr) const;
    bool killPid(int pid);
    // spawnDetached 以「脱离父进程」的方式拉起后端：GUI 退出不会带走后端，
    // 也不会弹控制台黑框（Windows: CreateProcess + CREATE_NO_WINDOW；Linux: startDetached）。
    static bool spawnDetached(const QString &exe, const QString &workDir, qint64 *pid, QString *error);
    bool backendStopped(const RuntimeInfo &info, int timeoutMs) const;

    QString configPath_;
    mutable QString cachedText_;
    mutable QDateTime cachedMtime_;
    IpcHttpClient ipc_;
    BackendStatus lastStatus_;
};