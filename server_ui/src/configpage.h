#pragma once

#include "backendclient.h"

#include <QVector>
#include <QWidget>

#include <functional>

class QLabel;
class QPlainTextEdit;
class QPushButton;
class QScrollArea;
class QTabWidget;
class QVBoxLayout;

// ConfigPage 是「可填写的配置表单 + 原文兜底」两个页签：
//   - 表单：字段由后端 GET /config/fields 提供（分组/中文标签/类型/热字段标记），
//     只提交改动过的字段，保存后后端保留注释并备份；
//   - 原文：整份 YAML 直接编辑，保存前仍走后端 /config/validate 校验。
class ConfigPage : public QWidget {
    Q_OBJECT

public:
    explicit ConfigPage(BackendClient *client, QWidget *parent = nullptr);

    // reload 重新拉取字段目录并读一次原文。
    void reload();

public slots:
    void openFile();

signals:
    void saved();

private:
    // Entry 是一条表单字段：field 是后端描述，editor 是渲染出来的控件，get 取值。
    struct Entry {
        ConfigFieldInfo field;
        QWidget *editor = nullptr;
        std::function<QString()> get;
        QString original;
    };

    void buildForm(const ConfigForm &form);
    void clearForm();
    void showFormMessage(const QString &text, bool error);
    void saveCurrentTab();
    void saveForm();
    void saveRaw();
    void loadRawText();
    void updatePathLabel();
    QWidget *createEditor(const ConfigFieldInfo &field, std::function<QString()> &getter, QWidget *parent);
    QWidget *createFieldRow(const ConfigFieldInfo &field, QWidget *parent);
    void applySaveResult(bool ok, const QString &backup, const QStringList &hot,
                         const QStringList &cold, const QString &error);
    void saveRawDirectly();

    BackendClient *client_ = nullptr;
    QLabel *pathLabel_ = nullptr;
    QTabWidget *tabs_ = nullptr;
    QScrollArea *formScroll_ = nullptr;
    QWidget *formHost_ = nullptr;
    QVBoxLayout *formLayout_ = nullptr;
    QPlainTextEdit *rawEditor_ = nullptr;
    QPushButton *saveButton_ = nullptr;
    QVector<Entry> entries_;
    QString pendingRawText_;
};