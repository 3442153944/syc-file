#include "configfile.h"

#include <QDateTime>
#include <QDir>
#include <QFile>
#include <QFileInfo>
#include <QSaveFile>
#include <QThread>

namespace configfile {

QString readText(const QString &path, QString *error)
{
    QFile f(path);
    if (!f.open(QIODevice::ReadOnly | QIODevice::Text)) {
        if (error)
            *error = QStringLiteral("读取 %1 失败: %2").arg(path, f.errorString());
        return {};
    }
    return QString::fromUtf8(f.readAll());
}

bool saveText(const QString &path, const QString &text, QString *backupPath, QString *error)
{
    const QFileInfo fi(path);
    if (backupPath)
        backupPath->clear();

    if (fi.exists()) {
        const QString suffix = QDateTime::currentDateTime().toString(QStringLiteral("yyyyMMdd-HHmmss"));
        const QString backup = path + QStringLiteral(".bak-") + suffix;
        if (!QFile::copy(path, backup)) {
            if (error)
                *error = QStringLiteral("备份配置失败（无法写入 %1）").arg(backup);
            return false;
        }
        if (backupPath)
            *backupPath = backup;
    }

    QSaveFile out(path);
    if (!out.open(QIODevice::WriteOnly | QIODevice::Text)) {
        if (error)
            *error = QStringLiteral("写入配置失败: %1").arg(out.errorString());
        return false;
    }
    out.write(text.toUtf8());
    for (int i = 0; i < 20; ++i) {
        if (out.commit())
            return true;
        QThread::msleep(50);
    }
    if (error)
        *error = QStringLiteral("保存配置失败（文件被占用？）: %1").arg(out.errorString());
    return false;
}

} // namespace configfile