#include "backendclient.h"

#include <QCoreApplication>
#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QJsonArray>
#include <QJsonDocument>
#include <QJsonObject>
#include <QProcess>
#include <QRegularExpression>
#include <QTimer>

#ifdef Q_OS_WIN
#include <windows.h>
#else
#include <csignal>
#endif

namespace {

// 后端默认文件名与配置相对位置和 Go 版部署约定一致。
const char *kDefaultBackendExe = "syc-file.exe";
const char *kRuntimeFile = "runtime.json";

QString formatUptime(qint64 ns)
{
    if (ns <= 0)
        return QStringLiteral("—");
    const qint64 seconds = ns / 1000000000LL;
    const qint64 days = seconds / 86400;
    const qint64 hours = (seconds % 86400) / 3600;
    const qint64 minutes = (seconds % 3600) / 60;
    const qint64 secs = seconds % 60;
    if (days > 0)
        return QStringLiteral("%1天%2小时%3分").arg(days).arg(hours).arg(minutes);
    if (hours > 0)
        return QStringLiteral("%1小时%2分%3秒").arg(hours).arg(minutes).arg(secs);
    if (minutes > 0)
        return QStringLiteral("%1分%2秒").arg(minutes).arg(secs);
    return QStringLiteral("%1秒").arg(secs);
}

// yamlScalar 去掉标量值两侧的引号和行尾注释（GUI 只读 gui.* / log.path / server.port 这几个标量，
// 不引入 YAML 依赖；结构化编辑仍交给后端 /config/validate 兜底）。
QString yamlScalar(QString value)
{
    value = value.trimmed();
    if (value.size() >= 2) {
        const QChar first = value.front();
        const QChar last = value.back();
        if ((first == QLatin1Char('"') && last == QLatin1Char('"'))
            || (first == QLatin1Char('\'') && last == QLatin1Char('\''))) {
            return value.mid(1, value.size() - 2);
        }
    }
    const int comment = value.indexOf(QStringLiteral(" #"));
    if (comment > 0)
        value = value.left(comment).trimmed();
    return value;
}

QDateTime parseGoTime(const QString &raw)
{
    if (raw.isEmpty())
        return {};
    QDateTime dt = QDateTime::fromString(raw, Qt::ISODateWithMs);
    if (!dt.isValid())
        dt = QDateTime::fromString(raw, Qt::ISODate);
    if (!dt.isValid()) {
        // Go 的 RFC3339Nano 可能带 7 位小数，截断到 3 位再试一次。
        static const QRegularExpression re(QStringLiteral(R"((\.\d{3})\d*)"));
        QString fixed = raw;
        fixed.replace(re, QStringLiteral("\\1"));
        dt = QDateTime::fromString(fixed, Qt::ISODateWithMs);
    }
    return dt;
}

} // namespace

namespace {

BackendStatus offlineStatus(const QString &detail)
{
    BackendStatus status;
    status.detail = detail;
    return status;
}

} // namespace

BackendClient::BackendClient(const QString &configPath, QObject *parent)
    : QObject(parent), configPath_(QFileInfo(configPath).absoluteFilePath())
{
    // 通信只走本机 IPC（见 IpcHttpClient）：不占用端口，也不经过代理/虚拟网卡，
    // 从根上避免「代理 TUN 接管 loopback 导致连不上后端」。
}

QString BackendClient::runtimePath() const
{
    return QDir(configDir()).absoluteFilePath(QString::fromLatin1(kRuntimeFile));
}

QString BackendClient::configDir() const
{
    return QFileInfo(configPath_).absolutePath();
}

QString BackendClient::configText(QString *error) const
{
    QFileInfo fi(configPath_);
    if (cachedMtime_.isValid() && cachedMtime_ == fi.lastModified() && !cachedText_.isEmpty())
        return cachedText_;
    QFile f(configPath_);
    if (!f.open(QIODevice::ReadOnly | QIODevice::Text)) {
        if (error)
            *error = QStringLiteral("读取配置失败: %1").arg(f.errorString());
        return {};
    }
    cachedText_ = QString::fromUtf8(f.readAll());
    cachedMtime_ = fi.lastModified();
    return cachedText_;
}

// scalarValue 只在 GUI 自己需要的几个标量上做轻量解析（gui.* / log.*），
// 不引入 YAML 依赖；复杂的结构化编辑仍交给后端 /config/validate 兜底。
QString BackendClient::scalarValue(const QString &section, const QString &key, bool *found) const
{
    if (found)
        *found = false;
    const QString text = configText();
    const QStringList lines = text.split(QLatin1Char('\n'));
    bool inSection = false;
    int sectionIndent = 0;
    for (const QString &raw : lines) {
        const QString line = raw;
        const QString trimmed = line.trimmed();
        if (trimmed.isEmpty() || trimmed.startsWith(QLatin1Char('#')))
            continue;
        const int indent = line.size() - line.trimmed().size();
        if (!inSection) {
            if (trimmed == section + QLatin1Char(':')) {
                inSection = true;
                sectionIndent = indent;
            }
            continue;
        }
        if (indent <= sectionIndent && !trimmed.startsWith(QLatin1Char('-')))
            break;
        if (trimmed.startsWith(key + QLatin1Char(':'))) {
            const QString value = yamlScalar(trimmed.mid(key.size() + 1));
            if (found)
                *found = true;
            return value;
        }
    }
    return {};
}

bool BackendClient::startMinimized() const
{
    bool found = false;
    const QString v = scalarValue(QStringLiteral("gui"), QStringLiteral("start_minimized"), &found);
    if (!found)
        return false;
    const QString lower = v.toLower();
    return lower == QStringLiteral("true") || lower == QStringLiteral("yes") || lower == QStringLiteral("1");
}

QString BackendClient::backendExePath() const
{
    bool found = false;
    QString v = scalarValue(QStringLiteral("gui"), QStringLiteral("backend_path"), &found);
    if (found && !v.trimmed().isEmpty()) {
        QFileInfo fi(v);
        if (fi.isRelative())
            return QDir(QCoreApplication::applicationDirPath()).absoluteFilePath(v);
        return v;
    }
    return QDir(QCoreApplication::applicationDirPath()).absoluteFilePath(QString::fromLatin1(kDefaultBackendExe));
}

QString BackendClient::backendWorkDir() const
{
    bool found = false;
    QString v = scalarValue(QStringLiteral("gui"), QStringLiteral("backend_workdir"), &found);
    if (found && !v.trimmed().isEmpty()) {
        QFileInfo fi(v);
        if (fi.isRelative())
            return QDir(QCoreApplication::applicationDirPath()).absoluteFilePath(v);
        return v;
    }
    return QCoreApplication::applicationDirPath();
}

QString BackendClient::logDir(const BackendStatus &status) const
{
    if (status.valid && !status.logDir.isEmpty())
        return QDir::cleanPath(status.logDir);
    const QString cfgText = configText();
    const QStringList lines = cfgText.split(QLatin1Char('\n'));
    bool inLog = false;
    int indent0 = 0;
    QString path;
    for (const QString &raw : lines) {
        const QString trimmed = raw.trimmed();
        if (trimmed.isEmpty() || trimmed.startsWith(QLatin1Char('#')))
            continue;
        const int indent = raw.size() - trimmed.size();
        if (!inLog) {
            if (trimmed == QStringLiteral("log:")) {
                inLog = true;
                indent0 = indent;
            }
            continue;
        }
        if (indent <= indent0 && !trimmed.startsWith(QLatin1Char('-')))
            break;
        if (trimmed.startsWith(QStringLiteral("path:"))) {
            path = yamlScalar(trimmed.mid(5));
            break;
        }
    }
    if (path.isEmpty())
        path = QStringLiteral("./log");
    QFileInfo fi(path);
    if (fi.isRelative())
        return QDir::cleanPath(QDir(backendWorkDir()).absoluteFilePath(path));
    return QDir::cleanPath(path);
}

RuntimeInfo BackendClient::readRuntime() const
{
    RuntimeInfo info;
    QFile f(QDir(configDir()).absoluteFilePath(QString::fromLatin1(kRuntimeFile)));
    if (!f.open(QIODevice::ReadOnly))
        return info;
    const QJsonDocument doc = QJsonDocument::fromJson(f.readAll());
    if (!doc.isObject())
        return info;
    const QJsonObject o = doc.object();
    info.valid = true;
    info.pid = o.value(QStringLiteral("pid")).toInt();
    info.ipc = o.value(QStringLiteral("ipc")).toString();
    info.token = o.value(QStringLiteral("token")).toString();
    info.version = o.value(QStringLiteral("version")).toString();
    info.mode = o.value(QStringLiteral("mode")).toString();
    info.configPath = o.value(QStringLiteral("config_path")).toString();
    info.workingDir = o.value(QStringLiteral("working_dir")).toString();
    info.startedAt = parseGoTime(o.value(QStringLiteral("started_at")).toString());
    if (info.pid <= 0 || info.ipc.isEmpty() || info.token.isEmpty())
        info.valid = false;
    return info;
}

bool BackendClient::backendRunning() const
{
    // 以最近一次 /status 轮询为准；还没轮询过时用一次短超时的 IPC 探测兜底。
    if (lastStatus_.valid)
        return true;
    const RuntimeInfo info = readRuntime();
    return info.valid && IpcHttpClient::probe(info.ipc, 150);
}

bool BackendClient::backendStopped(const RuntimeInfo &info, int timeoutMs) const
{
    // 后端优雅退出时会删除 runtime.json；管道/socket 也会随之关闭。
    if (!QFileInfo::exists(runtimePath()))
        return true;
    if (!info.ipc.isEmpty() && !IpcHttpClient::probe(info.ipc, timeoutMs))
        return true;
    return false;
}

void BackendClient::getStatus(std::function<void(bool, const BackendStatus &)> done)
{
    const RuntimeInfo info = readRuntime();
    if (!info.valid) {
        const bool exists = QFileInfo::exists(runtimePath());
        done(false, offlineStatus(
                      exists ? QStringLiteral("runtime.json 内容不完整或缺 ipc（本机 IPC 端点），"
                                              "后端可能正在启动；也可能是旧版本后端。")
                             : QStringLiteral("找不到 %1：后端没启动，或本控制台指向的配置目录不对。")
                                   .arg(runtimePath())));
        return;
    }
    ipc_.setEndpoint(info.ipc, info.token);
    ipc_.get(QStringLiteral("/status"),
             [info, done](bool ok, int code, const QByteArray &body, const QString &error) {
                 if (!ok) {
                     BackendStatus status;
                     status.runtimePresent = true;
                     status.detail = QStringLiteral("连不上本地管理接口（IPC %1）：%2。"
                                                    "runtime.json 可能是上次运行留下的旧文件。")
                                         .arg(info.ipc, error);
                     done(false, status);
                     return;
                 }
                 if (code != 200) {
                     BackendStatus status;
                     status.runtimePresent = true;
                     if (code == 401) {
                         status.detail = QStringLiteral(
                             "本地接口鉴权失败（token 不匹配）：后端可能刚重启过，稍等几秒会自动刷新。");
                     } else {
                         status.detail =
                             QStringLiteral("本地管理接口返回 HTTP %1。").arg(code);
                     }
                     done(false, status);
                     return;
                 }
                 const QJsonDocument doc = QJsonDocument::fromJson(body);
                 if (!doc.isObject()) {
                     BackendStatus status;
                     status.runtimePresent = true;
                     status.detail = QStringLiteral("本地接口返回的不是合法 JSON，版本可能不匹配。");
                     done(false, status);
                     return;
                 }
                 const QJsonObject o = doc.object();
                 BackendStatus st;
                 st.valid = true;
                 st.runtimePresent = true;
        st.version = o.value(QStringLiteral("version")).toString();
        st.mode = o.value(QStringLiteral("mode")).toString();
        st.nodeName = o.value(QStringLiteral("node_name")).toString();
        st.platform = o.value(QStringLiteral("platform")).toString();
        st.arch = o.value(QStringLiteral("arch")).toString();
        st.goVersion = o.value(QStringLiteral("go_version")).toString();
        st.pid = o.value(QStringLiteral("pid")).toInt();
        st.port = o.value(QStringLiteral("port")).toInt();
        st.startedAt = parseGoTime(o.value(QStringLiteral("started_at")).toString());
        st.uptimeNs = static_cast<qint64>(o.value(QStringLiteral("uptime")).toDouble());
        st.running = o.value(QStringLiteral("running")).toBool();
        st.configPath = o.value(QStringLiteral("config_path")).toString();
        st.logDir = o.value(QStringLiteral("log_dir")).toString();
        st.workingDir = o.value(QStringLiteral("working_dir")).toString();
        st.memoryBytes = static_cast<quint64>(o.value(QStringLiteral("memory_bytes")).toDouble());
        st.goroutines = o.value(QStringLiteral("goroutines")).toInt();
        st.onlineConns = o.value(QStringLiteral("online_conns")).toInt();
        st.dbOK = o.value(QStringLiteral("db_ok")).toBool();
        st.redisOK = o.value(QStringLiteral("redis_ok")).toBool();
        for (const QJsonValue &value : o.value(QStringLiteral("supervisor")).toArray()) {
            const QJsonObject item = value.toObject();
            SupervisorInfo process;
            process.name = item.value(QStringLiteral("name")).toString();
            process.running = item.value(QStringLiteral("running")).toBool();
            process.pid = item.value(QStringLiteral("pid")).toInt();
            process.restarts = item.value(QStringLiteral("restarts")).toInt();
            process.lastError = item.value(QStringLiteral("last_error")).toString();
            st.supervisor.append(process);
        }
        done(true, st);
    });
}

void BackendClient::fetchStatus()
{
    // 失败时也要把 detail / runtimePresent 传出去，界面才能说清「为什么连不上」。
    getStatus([this](bool, const BackendStatus &st) {
        lastStatus_ = st;
        emit statusUpdated(st);
    });
}

void BackendClient::post(const QString &path, const QByteArray &body, const QString &action,
                         std::function<void(bool, const QString &)> done)
{
    Q_UNUSED(action);
    const RuntimeInfo info = readRuntime();
    if (!info.valid) {
        done(false, QStringLiteral("后端未运行（找不到可用的 runtime.json / IPC 端点）"));
        return;
    }
    ipc_.setEndpoint(info.ipc, info.token);
    ipc_.post(path, body, QByteArrayLiteral("text/plain; charset=utf-8"),
              [done](bool ok, int code, const QByteArray &responseBody, const QString &error) {
                  if (ok && (code == 200 || code == 202)) {
                      done(true, QString());
                      return;
                  }
                  QString msg = QString::fromUtf8(responseBody);
                  const QJsonDocument doc = QJsonDocument::fromJson(responseBody);
                  if (doc.isObject()) {
                      const QString e = doc.object().value(QStringLiteral("error")).toString();
                      if (!e.isEmpty())
                          msg = e;
                  }
                  if (msg.trimmed().isEmpty())
                      msg = error;
                  done(false, msg.isEmpty() ? QStringLiteral("请求失败") : msg);
              });
}

void BackendClient::validateConfig(const QString &text)
{
    post(QStringLiteral("/config/validate"), text.toUtf8(), QStringLiteral("validate"),
         [this](bool ok, const QString &msg) {
             emit validateFinished(ok, ok ? QStringLiteral("校验通过") : msg);
         });
}

void BackendClient::startBackend()
{
    if (lastStatus_.valid) {
        emit actionFinished(QStringLiteral("start"), true, QStringLiteral("后端已在运行"));
        return;
    }
    // 不靠进程枚举判断占位：直接探本机 IPC 端点。端点还能连上就认为后端还在跑，
    // 避免在「进程活着但接口暂时不通」时又拉起一个实例，导致端口/IPC 冲突。
    const RuntimeInfo runtime = readRuntime();
    if (runtime.valid && IpcHttpClient::probe(runtime.ipc, 300)) {
        emit actionFinished(
            QStringLiteral("start"), false,
            QStringLiteral("检测到本机 IPC 端点（%1）仍在响应，后端可能仍在运行（只是 /status 暂时不通）。"
                           "请先确认并停止旧进程，或重启 Windows 服务 FileSyncBackend。")
                .arg(runtime.ipc));
        return;
    }
    const QString exe = backendExePath();
    if (!QFileInfo::exists(exe)) {
        emit actionFinished(QStringLiteral("start"), false,
                            QStringLiteral("找不到后端程序: %1").arg(exe));
        return;
    }
    qint64 spawnedPid = 0;
    QString spawnError;
    if (!spawnDetached(exe, backendWorkDir(), &spawnedPid, &spawnError)) {
        emit actionFinished(QStringLiteral("start"), false,
                            QStringLiteral("启动后端失败：%1").arg(spawnError));
        return;
    }
    emit actionFinished(QStringLiteral("start"), true,
                        QStringLiteral("后端已启动（pid=%1），正在等待本地接口就绪…").arg(spawnedPid));
}

bool BackendClient::spawnDetached(const QString &exe, const QString &workDir, qint64 *pid, QString *error)
{
    if (error)
        error->clear();
#ifdef Q_OS_WIN
    // 不能用 QProcess：QProcess 析构时会杀掉还在运行的子进程，后端会刚拉起就消失。
    // CreateProcess + CREATE_NO_WINDOW 既能脱离父进程，又不会弹控制台黑框。
    std::wstring commandLine = QStringLiteral("\"%1\"").arg(QDir::toNativeSeparators(exe)).toStdWString();
    const QString nativeWorkDir = QDir::toNativeSeparators(workDir);

    STARTUPINFOW startupInfo;
    ZeroMemory(&startupInfo, sizeof(startupInfo));
    startupInfo.cb = sizeof(startupInfo);
    startupInfo.dwFlags = STARTF_USESHOWWINDOW;
    startupInfo.wShowWindow = SW_HIDE;

    PROCESS_INFORMATION processInfo;
    ZeroMemory(&processInfo, sizeof(processInfo));

    const BOOL ok = CreateProcessW(nullptr, commandLine.data(), nullptr, nullptr, FALSE, CREATE_NO_WINDOW,
                                   nullptr,
                                   nativeWorkDir.isEmpty()
                                       ? nullptr
                                       : reinterpret_cast<LPCWSTR>(nativeWorkDir.utf16()),
                                   &startupInfo, &processInfo);
    if (!ok) {
        if (error) {
            const DWORD code = GetLastError();
            LPWSTR buffer = nullptr;
            FormatMessageW(FORMAT_MESSAGE_ALLOCATE_BUFFER | FORMAT_MESSAGE_FROM_SYSTEM
                               | FORMAT_MESSAGE_IGNORE_INSERTS,
                           nullptr, code, MAKELANGID(LANG_NEUTRAL, SUBLANG_DEFAULT),
                           reinterpret_cast<LPWSTR>(&buffer), 0, nullptr);
            *error = buffer ? QString::fromWCharArray(buffer).trimmed()
                            : QStringLiteral("错误码 %1").arg(code);
            if (buffer)
                LocalFree(buffer);
        }
        return false;
    }
    if (pid)
        *pid = static_cast<qint64>(processInfo.dwProcessId);
    CloseHandle(processInfo.hThread);
    CloseHandle(processInfo.hProcess);
    return true;
#else
    qint64 spawnedPid = 0;
    const bool ok = QProcess::startDetached(exe, QStringList(), workDir, &spawnedPid);
    if (!ok) {
        if (error)
            *error = QStringLiteral("无法启动进程");
        return false;
    }
    if (pid)
        *pid = spawnedPid;
    return true;
#endif
}

bool BackendClient::killPid(int pid)
{
    if (pid <= 0)
        return false;
#ifdef Q_OS_WIN
    HANDLE handle = OpenProcess(PROCESS_TERMINATE, FALSE, static_cast<DWORD>(pid));
    if (!handle)
        return false;
    const bool ok = TerminateProcess(handle, 0) != 0;
    CloseHandle(handle);
    return ok;
#else
    return ::kill(pid, SIGKILL) == 0;
#endif
}

void BackendClient::stopBackend()
{
    const RuntimeInfo info = readRuntime();
    if (!info.valid) {
        emit actionFinished(QStringLiteral("stop"), true, QStringLiteral("后端本来就没有运行"));
        return;
    }
    post(QStringLiteral("/shutdown"), {}, QStringLiteral("stop"),
         [this, info](bool ok, const QString &msg) {
             if (!ok)
                 emit actionFinished(QStringLiteral("stop"), false, msg);
             // 等后端退出（runtime.json 删除 / localapi 端口关闭）；5 秒还没退就强杀。
             auto *waited = new QTimer(this);
             waited->setInterval(200);
             auto ticks = std::make_shared<int>(0);
             connect(waited, &QTimer::timeout, this, [this, info, waited, ticks]() {
                 ++(*ticks);
                 const bool stopped = backendStopped(info, 120);
                 if (!stopped && *ticks <= 25)
                     return;
                 waited->stop();
                 waited->deleteLater();
                 if (stopped) {
                     emit actionFinished(QStringLiteral("stop"), true, QStringLiteral("后端已停止"));
                     return;
                 }
                 if (killPid(info.pid)) {
                     emit actionFinished(QStringLiteral("stop"), true, QStringLiteral("后端已强制停止"));
                 } else {
                     emit actionFinished(
                         QStringLiteral("stop"), false,
                         QStringLiteral("后端没有在 5 秒内退出，当前权限也无法结束该进程。"
                                        "如果它是 Windows 服务，请在「服务」里停止 FileSyncBackend。"));
                 }
             });
             waited->start();
         });
}

void BackendClient::restartBackend()
{
    const RuntimeInfo info = readRuntime();
    if (!info.valid) {
        startBackend();
        return;
    }
    // 先发 /shutdown，等旧进程退出后再静默拉起新进程。
    post(QStringLiteral("/shutdown"), {}, QStringLiteral("restart"),
         [this, info](bool ok, const QString &msg) {
             if (!ok) {
                 emit actionFinished(QStringLiteral("restart"), false, msg);
                 return;
             }
             auto *waited = new QTimer(this);
             waited->setInterval(200);
             auto ticks = std::make_shared<int>(0);
             connect(waited, &QTimer::timeout, this, [this, info, waited, ticks]() {
                 ++(*ticks);
                 const bool stopped = backendStopped(info, 120);
                 if (!stopped && *ticks <= 40)
                     return;
                 waited->stop();
                 waited->deleteLater();
                 if (!stopped && !killPid(info.pid)) {
                     emit actionFinished(
                         QStringLiteral("restart"), false,
                         QStringLiteral("旧进程没有退出，当前权限也无法结束它。"
                                        "如果它是 Windows 服务，请先用「服务」停止 FileSyncBackend 再启动。"));
                     return;
                 }
                 startBackend();
                 emit actionFinished(QStringLiteral("restart"), true, QStringLiteral("后端已重启"));
             });
             waited->start();
         });
}

// ---- 配置表单 --------------------------------------------------------------

void BackendClient::fetchConfigFields()
{
    const RuntimeInfo info = readRuntime();
    if (!info.valid) {
        emit configFieldsLoaded(false, ConfigForm{},
                                QStringLiteral("后端未运行（找不到可用的 runtime.json / IPC 端点），无法读取配置表单"));
        return;
    }
    ipc_.setEndpoint(info.ipc, info.token);
    ipc_.get(QStringLiteral("/config/fields"),
             [this](bool ok, int code, const QByteArray &body, const QString &error) {
                 if (!ok) {
                     emit configFieldsLoaded(false, ConfigForm{}, error);
                     return;
                 }
                 if (code != 200) {
                     QString msg = QString::fromUtf8(body);
                     const QJsonDocument doc = QJsonDocument::fromJson(body);
                     if (doc.isObject())
                         msg = doc.object().value(QStringLiteral("error")).toString(msg);
                     emit configFieldsLoaded(false, ConfigForm{},
                                             msg.isEmpty() ? QStringLiteral("读取配置字段失败") : msg);
                     return;
                 }
                 const QJsonDocument doc = QJsonDocument::fromJson(body);
                 if (!doc.isObject()) {
                     emit configFieldsLoaded(false, ConfigForm{}, QStringLiteral("配置字段响应格式不对"));
                     return;
                 }
                 const QJsonObject root = doc.object();
                 ConfigForm form;
                 form.valid = true;
                 form.path = root.value(QStringLiteral("path")).toString();
                 const QJsonArray fields = root.value(QStringLiteral("fields")).toArray();
                 form.fields.reserve(fields.size());
                 for (const QJsonValue &value : fields) {
                     const QJsonObject o = value.toObject();
                     ConfigFieldInfo field;
                     field.path = o.value(QStringLiteral("path")).toString();
                     field.label = o.value(QStringLiteral("label")).toString();
                     field.group = o.value(QStringLiteral("group")).toString();
                     field.kind = o.value(QStringLiteral("kind")).toString();
                     field.value = o.value(QStringLiteral("value")).toString();
                     field.description = o.value(QStringLiteral("description")).toString();
                     field.hotReload = o.value(QStringLiteral("hot_reload")).toBool();
                     field.readOnly = o.value(QStringLiteral("read_only")).toBool();
                     for (const QJsonValue &opt : o.value(QStringLiteral("options")).toArray())
                         field.options << opt.toString();
                     form.fields.append(field);
                 }
                 emit configFieldsLoaded(true, form, QString());
             });
}

void BackendClient::saveConfigFields(const QMap<QString, QString> &values)
{
    const RuntimeInfo info = readRuntime();
    if (!info.valid) {
        emit configSaved(false, QString(), {}, {},
                         QStringLiteral("后端未运行（找不到可用的 runtime.json / IPC 端点）"));
        return;
    }
    QJsonObject valueObject;
    for (auto it = values.constBegin(); it != values.constEnd(); ++it)
        valueObject.insert(it.key(), it.value());
    QJsonObject root;
    root.insert(QStringLiteral("values"), valueObject);

    ipc_.setEndpoint(info.ipc, info.token);
    ipc_.post(QStringLiteral("/config/fields"), QJsonDocument(root).toJson(QJsonDocument::Compact),
              QByteArrayLiteral("application/json"),
              [this](bool ok, int code, const QByteArray &body, const QString &error) {
                  if (!ok) {
                      emit configSaved(false, QString(), {}, {}, error);
                      return;
                  }
                  const QJsonDocument doc = QJsonDocument::fromJson(body);
                  const QJsonObject o = doc.object();
                  if (code != 200) {
                      QString msg = o.value(QStringLiteral("error")).toString();
                      if (msg.isEmpty())
                          msg = QStringLiteral("保存配置失败");
                      emit configSaved(false, QString(), {}, {}, msg);
                      return;
                  }
                  QStringList hot;
                  QStringList cold;
                  for (const QJsonValue &v : o.value(QStringLiteral("hot_applied")).toArray())
                      hot << v.toString();
                  for (const QJsonValue &v : o.value(QStringLiteral("need_restart")).toArray())
                      cold << v.toString();
                  emit configSaved(true, o.value(QStringLiteral("backup_path")).toString(), hot, cold,
                                   QString());
              });
}