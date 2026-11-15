#pragma once

#include <QString>

// configfile 只负责「读原文 / 备份后原子写」，YAML 语义校验交给后端 /config/validate。
namespace configfile {

// readText 读取 UTF-8 配置文本；失败时 error 里是可展示的原因。
QString readText(const QString &path, QString *error = nullptr);

// saveText 先把现有文件复制成 config.yaml.bak-yyyyMMdd-HHmmss，
// 再写临时文件并原子替换。成功返回 true，backupPath 为备份路径（可能为空）。
bool saveText(const QString &path, const QString &text, QString *backupPath = nullptr,
              QString *error = nullptr);

} // namespace configfile