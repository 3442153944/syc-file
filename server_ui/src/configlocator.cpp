#include "configlocator.h"

#include <QCoreApplication>
#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QSettings>
#include <QStringList>

#ifdef Q_OS_WIN
#include <windows.h>
#include <tlhelp32.h>
#endif

namespace {

QString configInDir(const QString &dir)
{
    if (dir.trimmed().isEmpty())
        return {};
    const QString candidate = QDir(dir).filePath(QStringLiteral("config/config.yaml"));
    const QFileInfo info(candidate);
    return info.isFile() ? QDir::cleanPath(info.absoluteFilePath()) : QString();
}

QString configBesideExecutable(const QString &raw)
{
    QString path = raw.trimmed();
    if (path.isEmpty())
        return {};

    if (path.startsWith(QLatin1Char('"'))) {
        const int end = path.indexOf(QLatin1Char('"'), 1);
        if (end > 1)
            path = path.mid(1, end - 1);
    } else {
        // 未加引号的 ImagePath 可能带参数；路径里有空格时只能按第一个空格截断（Windows 服务通常加引号）。
        const int space = path.indexOf(QLatin1Char(' '));
        if (space > 0)
            path = path.left(space);
    }

    path.replace(QLatin1Char('\\'), QLatin1Char('/'));
    if (path.startsWith(QStringLiteral("\\??\\")) || path.startsWith(QStringLiteral("//?/")))
        path.remove(0, 4);
    if (path.startsWith(QLatin1String("\\SystemRoot\\")))
        path = qEnvironmentVariable("SystemRoot") + path.mid(11);

    return configInDir(QFileInfo(path).absolutePath());
}

QStringList splitCommandLine(const QString &line)
{
    QStringList tokens;
    QString current;
    bool quoted = false;
    for (const QChar ch : line) {
        if (ch == QLatin1Char('"')) {
            quoted = !quoted;
            continue;
        }
        if (!quoted && ch.isSpace()) {
            if (!current.isEmpty()) {
                tokens << current;
                current.clear();
            }
            continue;
        }
        current += ch;
    }
    if (!current.isEmpty())
        tokens << current;
    return tokens;
}

QString configFromTokens(const QStringList &tokens)
{
    for (int i = 0; i < tokens.size(); ++i) {
        const QString token = tokens.at(i);
        QString value;
        if (token == QLatin1String("-config") || token == QLatin1String("--config")) {
            if (i + 1 < tokens.size())
                value = tokens.at(i + 1);
        } else if (token.startsWith(QLatin1String("--config=")) || token.startsWith(QLatin1String("-config="))) {
            value = token.section(QLatin1Char('='), 1);
        }
        if (value.isEmpty())
            continue;
        const QFileInfo info(value);
        if (info.isFile())
            return QDir::cleanPath(info.absoluteFilePath());
        if (const QString beside = configInDir(info.absolutePath()); !beside.isEmpty())
            return beside;
    }
    return {};
}

#ifdef Q_OS_WIN

QString discoverFromService(const QString &serviceName)
{
    const QString base = QStringLiteral("HKEY_LOCAL_MACHINE\\SYSTEM\\CurrentControlSet\\Services\\") + serviceName;

    // nssm 服务：ImagePath 指向 nssm.exe，真正的后端在 Parameters\Application/AppDirectory。
    QSettings params(base + QStringLiteral("\\Parameters"), QSettings::NativeFormat);
    if (const QString found = configInDir(params.value(QStringLiteral("AppDirectory")).toString()); !found.isEmpty())
        return found;

    const QString app = params.value(QStringLiteral("Application")).toString();
    if (!app.isEmpty()) {
        if (const QString found = configInDir(QFileInfo(app).absolutePath()); !found.isEmpty())
            return found;
    }

    const QString args = params.value(QStringLiteral("AppParameters")).toString();
    if (const QString fromArgs = configFromTokens(splitCommandLine(args)); !fromArgs.isEmpty())
        return fromArgs;

    // 直接注册 exe 的服务：ImagePath 就是后端路径（可能带引号/参数）。
    QSettings service(base, QSettings::NativeFormat);
    const QString image = service.value(QStringLiteral("ImagePath")).toString();
    if (!image.isEmpty()) {
        if (const QString found = configBesideExecutable(image); !found.isEmpty())
            return found;
    }
    return {};
}

// 运行时判断：找正在运行的 syc-file.exe，用它的可执行文件位置定位 config.yaml。
// 不假设安装目录（服务 / 用户进程 / 自定义路径都能覆盖）。
QString discoverFromRunningProcess()
{
    HANDLE snapshot = CreateToolhelp32Snapshot(TH32CS_SNAPPROCESS, 0);
    if (snapshot == INVALID_HANDLE_VALUE)
        return {};

    QString result;
    PROCESSENTRY32W entry{};
    entry.dwSize = sizeof(entry);
    for (BOOL ok = Process32FirstW(snapshot, &entry); ok; ok = Process32NextW(snapshot, &entry)) {
        const QString name = QString::fromWCharArray(entry.szExeFile);
        if (name.compare(QStringLiteral("syc-file.exe"), Qt::CaseInsensitive) != 0)
            continue;
        HANDLE process = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, FALSE, entry.th32ProcessID);
        if (!process)
            continue;
        wchar_t buffer[32768];
        DWORD size = 32768;
        if (QueryFullProcessImageNameW(process, 0, buffer, &size)) {
            const QString exePath = QString::fromWCharArray(buffer, static_cast<int>(size));
            result = configInDir(QFileInfo(exePath).absolutePath());
        }
        CloseHandle(process);
        if (!result.isEmpty())
            break;
    }
    CloseHandle(snapshot);
    return result;
}

#else

// Linux/macOS：扫描 /proc，用正在运行的后端进程的 exe/cwd/cmdline 定位配置。
QString discoverFromRunningProcess()
{
    const QDir proc(QStringLiteral("/proc"));
    const QStringList entries = proc.entryList(QDir::Dirs | QDir::NoDotAndDotDot);
    for (const QString &entry : entries) {
        bool ok = false;
        entry.toInt(&ok);
        if (!ok)
            continue;

        const QString exe = QFile::symLinkTarget(QStringLiteral("/proc/%1/exe").arg(entry));
        if (exe.isEmpty() || QFileInfo(exe).fileName() != QStringLiteral("syc-file"))
            continue;

        if (const QString found = configInDir(QFileInfo(exe).absolutePath()); !found.isEmpty())
            return found;

        const QString cwd = QFile::symLinkTarget(QStringLiteral("/proc/%1/cwd").arg(entry));
        if (const QString found = configInDir(cwd); !found.isEmpty())
            return found;

        QFile cmdLine(QStringLiteral("/proc/%1/cmdline").arg(entry));
        if (cmdLine.open(QIODevice::ReadOnly)) {
            const QStringList tokens =
                QString::fromLocal8Bit(cmdLine.readAll()).split(QChar('\0'), Qt::SkipEmptyParts);
            if (const QString fromArgs = configFromTokens(tokens); !fromArgs.isEmpty())
                return fromArgs;
        }
    }
    return {};
}

#endif

} // namespace

QString configlocator::discover()
{
#ifdef Q_OS_WIN
    // 1) 服务注册（服务已安装但没运行时也能拿到后端位置）。
    const QStringList services = {
        QStringLiteral("FileSyncBackend"), QStringLiteral("SycFileBackend"),
        QStringLiteral("syc-file"), QStringLiteral("syc-file-backend"),
    };
    for (const QString &name : services) {
        if (const QString found = discoverFromService(name); !found.isEmpty())
            return found;
    }
#endif

    // 2) 正在运行的后端进程（非服务部署 / 自定义安装路径）。
    if (const QString found = discoverFromRunningProcess(); !found.isEmpty())
        return found;

    return {};
}

QString configlocator::resolve(const QString &explicitPath)
{
    if (!explicitPath.trimmed().isEmpty()) {
        const QFileInfo info(explicitPath);
        // 外部显式指定的路径优先；但安装器/快捷方式里的路径可能在后端搬家后失效，
        // 这时回退到运行时发现，而不是让控制台卡在一个不存在的路径上。
        if (info.exists())
            return info.absoluteFilePath();
        if (const QString found = discover(); !found.isEmpty())
            return found;
        return info.absoluteFilePath();
    }

    // 同目录部署优先：控制台和后端放在一起时行为与以前一致。
    const QString local = QDir(QCoreApplication::applicationDirPath())
                              .absoluteFilePath(QStringLiteral("config/config.yaml"));
    if (QFileInfo::exists(local))
        return QDir::cleanPath(local);

    if (const QString found = discover(); !found.isEmpty())
        return found;

    // 兜底：保持旧默认路径，让界面能显示“配置文件不存在/后端未运行”的具体路径。
    return QDir::cleanPath(local);
}