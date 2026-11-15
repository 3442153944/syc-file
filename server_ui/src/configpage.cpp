#include "configpage.h"

#include "configfile.h"
#include "icons.h"
#include "theme.h"
#include "widgets.h"

#include <QCheckBox>
#include <QComboBox>
#include <QDesktopServices>
#include <QFileInfo>
#include <QFontDatabase>
#include <QHBoxLayout>
#include <QLabel>
#include <QLineEdit>
#include <QMainWindow>
#include <QMessageBox>
#include <QPlainTextEdit>
#include <QPushButton>
#include <QScrollArea>
#include <QScrollBar>
#include <QStatusBar>
#include <QTabWidget>
#include <QTimer>
#include <QUrl>
#include <QVBoxLayout>
#include <QWheelEvent>

namespace {

// WheelForwardFilter 把多行输入框上的滚轮转给页面滚动区：
// 否则鼠标停在“允许的扩展名”这类列表框上时，滚轮只会滚框内内容，整页不动。
class WheelForwardFilter : public QObject {
public:
    WheelForwardFilter(QScrollArea *area, QObject *parent) : QObject(parent), area_(area) {}

protected:
    bool eventFilter(QObject *watched, QEvent *event) override
    {
        if (event->type() == QEvent::Wheel && area_) {
            auto *wheel = static_cast<QWheelEvent *>(event);
            auto *bar = area_->verticalScrollBar();
            bar->setValue(bar->value() - wheel->angleDelta().y() / 2);
            return true;
        }
        return QObject::eventFilter(watched, event);
    }

private:
    QScrollArea *area_ = nullptr;
};

} // namespace

ConfigPage::ConfigPage(BackendClient *client, QWidget *parent)
    : QWidget(parent), client_(client)
{
    setObjectName(QStringLiteral("Page"));

    auto *outer = new QVBoxLayout(this);
    outer->setContentsMargins(24, 20, 24, 24);
    outer->setSpacing(16);

    // ---- 顶部：路径 + 操作 ----
    auto *topCard = new ui::Card(this);
    topCard->setTitle(QStringLiteral("配置文件"), QStringLiteral("保存前由后端校验，自动备份为 .bak-时间戳"));
    auto *topRow = new QHBoxLayout();
    topRow->setContentsMargins(0, 0, 0, 0);
    topRow->setSpacing(10);

    pathLabel_ = new QLabel(client_->configPath(), topCard);
    pathLabel_->setObjectName(QStringLiteral("PathValue"));
    pathLabel_->setTextInteractionFlags(Qt::TextSelectableByMouse);
    pathLabel_->setSizePolicy(QSizePolicy::Expanding, QSizePolicy::Preferred);
    pathLabel_->setWordWrap(true);

    auto *reloadButton = new QPushButton(icons::make(icons::Glyph::Refresh, 16), QStringLiteral("重新加载"), topCard);
    auto *openButton = new QPushButton(icons::make(icons::Glyph::OpenFile, 16), QStringLiteral("打开文件"), topCard);
    saveButton_ = new QPushButton(QStringLiteral("保存"), topCard);
    saveButton_->setObjectName(QStringLiteral("Primary"));
    saveButton_->setDefault(true);
    saveButton_->setEnabled(false);
    for (QPushButton *button : {reloadButton, openButton, saveButton_})
        button->setCursor(Qt::PointingHandCursor);

    topRow->addWidget(pathLabel_, 1);
    topRow->addWidget(reloadButton);
    topRow->addWidget(openButton);
    topRow->addWidget(saveButton_);
    topCard->addBodyLayout(topRow);
    outer->addWidget(topCard);

    // ---- 页签：表单 / 原文 ----
    tabs_ = new QTabWidget(this);

    formScroll_ = new QScrollArea(tabs_);
    formScroll_->setWidgetResizable(true);
    formScroll_->setFrameShape(QFrame::NoFrame);
    formScroll_->setHorizontalScrollBarPolicy(Qt::ScrollBarAlwaysOff);
    formScroll_->viewport()->setAutoFillBackground(false);
    formHost_ = new QWidget(formScroll_);
    formHost_->setObjectName(QStringLiteral("PageContent"));
    formLayout_ = new QVBoxLayout(formHost_);
    formLayout_->setContentsMargins(0, 8, 4, 8);
    formLayout_->setSpacing(16);
    formScroll_->setWidget(formHost_);
    tabs_->addTab(formScroll_, QStringLiteral("表单"));

    auto *rawPage = new QWidget(tabs_);
    auto *rawLayout = new QVBoxLayout(rawPage);
    rawLayout->setContentsMargins(0, 8, 0, 0);
    rawLayout->setSpacing(10);

    auto *rawCard = new ui::Card(rawPage);
    rawCard->setTitle(QStringLiteral("config.yaml 原文"),
                      QStringLiteral("整份文件；保存前仍走后端 /config/validate 校验"));
    rawEditor_ = new QPlainTextEdit(rawCard);
    rawEditor_->setObjectName(QStringLiteral("ConfigText"));
    rawEditor_->setTabStopDistance(rawEditor_->fontMetrics().horizontalAdvance(QLatin1Char(' ')) * 2);
    rawEditor_->setLineWrapMode(QPlainTextEdit::NoWrap);
    rawCard->addBodyWidget(rawEditor_, 1);
    auto *rawHint = new QLabel(
        QStringLiteral("提示：保存会自动备份 config.yaml.bak-时间戳；如果 YAML 不合法，后端会拒绝保存。"),
        rawCard);
    rawHint->setObjectName(QStringLiteral("Hint"));
    rawHint->setWordWrap(true);
    rawCard->addBodyWidget(rawHint);
    rawLayout->addWidget(rawCard, 1);
    tabs_->addTab(rawPage, QStringLiteral("原文编辑"));

    outer->addWidget(tabs_, 1);

    connect(saveButton_, &QPushButton::clicked, this, &ConfigPage::saveCurrentTab);
    connect(reloadButton, &QPushButton::clicked, this, &ConfigPage::reload);
    connect(openButton, &QPushButton::clicked, this, &ConfigPage::openFile);
    connect(client_, &BackendClient::configFieldsLoaded, this,
            [this](bool ok, const ConfigForm &form, const QString &error) {
                if (!ok) {
                    showFormMessage(QStringLiteral("读取配置字段失败：%1").arg(error), true);
                    return;
                }
                buildForm(form);
            });
    connect(client_, &BackendClient::configSaved, this, &ConfigPage::applySaveResult);
    connect(client_, &BackendClient::validateFinished, this, [this](bool ok, const QString &message) {
        if (!ok) {
            QMessageBox::warning(this, QStringLiteral("配置校验失败"), message);
            return;
        }
        saveRawDirectly();
    });

    reload();
}

void ConfigPage::reload()
{
    updatePathLabel();
    loadRawText();
    saveButton_->setEnabled(false);
    client_->fetchConfigFields();
}

void ConfigPage::updatePathLabel()
{
    pathLabel_->setText(client_->configPath());
    pathLabel_->setToolTip(QStringLiteral("最后修改：%1")
                               .arg(QFileInfo(client_->configPath())
                                        .lastModified()
                                        .toString(QStringLiteral("yyyy-MM-dd HH:mm:ss"))));
}

void ConfigPage::loadRawText()
{
    QString error;
    const QString text = configfile::readText(client_->configPath(), &error);
    if (!error.isEmpty()) {
        rawEditor_->setPlainText(QStringLiteral("# %1").arg(error));
        return;
    }
    if (rawEditor_->toPlainText() != text)
        rawEditor_->setPlainText(text);
}

void ConfigPage::clearForm()
{
    entries_.clear();
    while (QLayoutItem *item = formLayout_->takeAt(0)) {
        if (QWidget *widget = item->widget()) {
            widget->hide();
            widget->deleteLater();
        }
        delete item;
    }
}

void ConfigPage::showFormMessage(const QString &text, bool error)
{
    clearForm();
    auto *label = new QLabel(text, formHost_);
    label->setWordWrap(true);
    label->setStyleSheet(error ? QStringLiteral("color:#B91C1C;") : QStringLiteral("color:#64748B;"));
    formLayout_->addWidget(label);
    formLayout_->addStretch(1);
}

void ConfigPage::buildForm(const ConfigForm &form)
{
    clearForm();
    auto *summary = new QLabel(
        QStringLiteral("共 %1 个可编辑项 · 只提交改动过的字段 · 绿色「即时生效」保存后立刻生效，"
                       "橙色「需重启」要等右上角「重启后端」")
            .arg(form.fields.size()),
        formHost_);
    summary->setObjectName(QStringLiteral("Hint"));
    summary->setWordWrap(true);
    formLayout_->addWidget(summary);

    ui::Card *card = nullptr;
    QString currentGroup;
    for (const ConfigFieldInfo &field : form.fields) {
        if (!card || field.group != currentGroup) {
            currentGroup = field.group;
            card = new ui::Card(formHost_);
            card->setTitle(currentGroup.isEmpty() ? QStringLiteral("常规") : currentGroup);
            formLayout_->addWidget(card);
        }
        card->addBodyWidget(createFieldRow(field, card));
    }
    formLayout_->addStretch(1);
}

QWidget *ConfigPage::createFieldRow(const ConfigFieldInfo &field, QWidget *parent)
{
    auto *row = new QWidget(parent);
    auto *layout = new QHBoxLayout(row);
    layout->setContentsMargins(0, 0, 0, 0);
    layout->setSpacing(24);

    // 左列：中文名 + 生效方式徽标
    auto *leftHost = new QWidget(row);
    leftHost->setFixedWidth(196);
    auto *left = new QVBoxLayout(leftHost);
    left->setContentsMargins(0, 2, 0, 0);
    left->setSpacing(6);
    auto *name = new QLabel(field.label.isEmpty() ? field.path : field.label, leftHost);
    name->setObjectName(QStringLiteral("FieldLabel"));
    name->setWordWrap(true);
    name->setToolTip(field.path);
    left->addWidget(name);
    ui::Pill *badge = nullptr;
    if (field.readOnly)
        badge = new ui::Pill(QStringLiteral("只读"), ui::Pill::Neutral, leftHost);
    else if (field.hotReload)
        badge = new ui::Pill(QStringLiteral("即时生效"), ui::Pill::Success, leftHost);
    else
        badge = new ui::Pill(QStringLiteral("需重启"), ui::Pill::Warning, leftHost);
    left->addWidget(badge, 0, Qt::AlignLeft);
    left->addStretch(1);

    // 右列：编辑控件 + 说明
    std::function<QString()> getter;
    auto *rightHost = new QWidget(row);
    auto *right = new QVBoxLayout(rightHost);
    right->setContentsMargins(0, 0, 0, 0);
    right->setSpacing(4);
    QWidget *editor = createEditor(field, getter, rightHost);
    right->addWidget(editor);
    if (!field.description.isEmpty()) {
        auto *description = new QLabel(field.description, rightHost);
        description->setObjectName(QStringLiteral("Hint"));
        description->setWordWrap(true);
        right->addWidget(description);
    }
    right->addStretch(1);
    if (field.readOnly)
        editor->setEnabled(false);

    layout->addWidget(leftHost, 0, Qt::AlignTop);
    layout->addWidget(rightHost, 1);

    Entry entry;
    entry.field = field;
    entry.editor = editor;
    entry.get = getter;
    entry.original = field.value;
    entries_.append(entry);

    // 任何编辑都启用保存按钮（真正有无变化在 saveForm 里再判断）。
    if (auto *lineEdit = qobject_cast<QLineEdit *>(editor)) {
        connect(lineEdit, &QLineEdit::textEdited, this, [this]() { saveButton_->setEnabled(true); });
    } else if (auto *combo = qobject_cast<QComboBox *>(editor)) {
        connect(combo, &QComboBox::currentTextChanged, this, [this]() { saveButton_->setEnabled(true); });
    } else if (auto *check = qobject_cast<QCheckBox *>(editor)) {
        connect(check, &QCheckBox::toggled, this, [this]() { saveButton_->setEnabled(true); });
    } else if (auto *plain = qobject_cast<QPlainTextEdit *>(editor)) {
        connect(plain, &QPlainTextEdit::textChanged, this, [this]() { saveButton_->setEnabled(true); });
    }
    return row;
}

QWidget *ConfigPage::createEditor(const ConfigFieldInfo &field, std::function<QString()> &getter, QWidget *parent)
{
    if (field.kind == QStringLiteral("bool")) {
        auto *check = new QCheckBox(QStringLiteral("启用"), parent);
        check->setChecked(field.value.compare(QStringLiteral("true"), Qt::CaseInsensitive) == 0);
        getter = [check]() { return check->isChecked() ? QStringLiteral("true") : QStringLiteral("false"); };
        return check;
    }
    if (field.kind == QStringLiteral("select")) {
        auto *combo = new QComboBox(parent);
        combo->addItems(field.options);
        if (!field.value.isEmpty() && combo->findText(field.value) < 0)
            combo->addItem(field.value);
        combo->setCurrentText(field.value);
        getter = [combo]() { return combo->currentText(); };
        return combo;
    }
    if (field.kind == QStringLiteral("list") || field.kind == QStringLiteral("text")) {
        auto *edit = new QPlainTextEdit(parent);
        edit->setPlainText(field.value);
        edit->setFixedHeight(field.kind == QStringLiteral("list") ? 88 : 72);
        edit->setTabChangesFocus(true);
        edit->installEventFilter(new WheelForwardFilter(formScroll_, edit));
        getter = [edit]() { return edit->toPlainText(); };
        return edit;
    }
    if (field.kind == QStringLiteral("secret")) {
        auto *edit = new QLineEdit(parent);
        edit->setEchoMode(QLineEdit::Password);
        edit->setPlaceholderText(QStringLiteral("已设置；留空表示不修改"));
        getter = [edit]() { return edit->text(); };
        return edit;
    }
    auto *edit = new QLineEdit(parent);
    edit->setText(field.value);
    edit->setPlaceholderText(QStringLiteral("请输入 %1").arg(field.label));
    getter = [edit]() { return edit->text(); };
    return edit;
}

void ConfigPage::saveCurrentTab()
{
    if (tabs_->currentIndex() == 1)
        saveRaw();
    else
        saveForm();
}

void ConfigPage::saveForm()
{
    QMap<QString, QString> values;
    for (const Entry &entry : entries_) {
        const QString value = entry.get();
        if (entry.field.kind == QStringLiteral("secret")) {
            // 密码类留空 = 不修改；只有用户真的输入了新值才提交。
            if (value.isEmpty())
                continue;
        } else if (value == entry.original) {
            continue;
        }
        values.insert(entry.field.path, value);
    }
    if (values.isEmpty()) {
        QMessageBox::information(this, QStringLiteral("没有改动"),
                                 QStringLiteral("表单里没有修改过的字段。"));
        return;
    }
    saveButton_->setEnabled(false);
    if (auto *main = qobject_cast<QMainWindow *>(window()))
        main->statusBar()->showMessage(QStringLiteral("正在保存 %1 个字段…").arg(values.size()));
    client_->saveConfigFields(values);
}

void ConfigPage::applySaveResult(bool ok, const QString &backup, const QStringList &hot,
                                 const QStringList &cold, const QString &error)
{
    saveButton_->setEnabled(true);
    if (!ok) {
        QMessageBox::warning(this, QStringLiteral("保存失败"), error);
        return;
    }
    QString message = QStringLiteral("已保存。");
    if (!backup.isEmpty())
        message += QStringLiteral("\n备份：%1").arg(backup);
    if (!hot.isEmpty())
        message += QStringLiteral("\n立即生效（%1）：%2").arg(hot.size()).arg(hot.join(QStringLiteral("、")));
    if (!cold.isEmpty()) {
        message += QStringLiteral("\n需重启才生效（%1）：%2").arg(cold.size()).arg(cold.join(QStringLiteral("、")));
        message += QStringLiteral("\n\n点右上角「重启后端」可以让这些字段生效。");
    }
    QMessageBox::information(this, QStringLiteral("配置已保存"), message);
    emit saved();
    // 热字段由后端 watcher 在 1s 内应用，稍等一下再刷新，避免又读到旧值。
    QTimer::singleShot(1200, this, [this]() { reload(); });
}

void ConfigPage::saveRaw()
{
    pendingRawText_ = rawEditor_->toPlainText();
    if (!client_->backendRunning()) {
        const auto answer = QMessageBox::question(
            this, QStringLiteral("后端未运行"),
            QStringLiteral("后端没在运行，无法校验 YAML。仍然保存吗？\n"
                           "（保存后若启动失败，可用 config.yaml.bak-* 回滚）"),
            QMessageBox::Yes | QMessageBox::No, QMessageBox::No);
        if (answer == QMessageBox::Yes)
            saveRawDirectly();
        return;
    }
    if (auto *main = qobject_cast<QMainWindow *>(window()))
        main->statusBar()->showMessage(QStringLiteral("正在校验配置…"));
    client_->validateConfig(pendingRawText_);
}

void ConfigPage::saveRawDirectly()
{
    QString backup;
    QString error;
    if (!configfile::saveText(client_->configPath(), pendingRawText_, &backup, &error)) {
        QMessageBox::warning(this, QStringLiteral("保存失败"), error);
        return;
    }
    emit saved();
    const QString message = backup.isEmpty()
                                ? QStringLiteral("配置已保存")
                                : QStringLiteral("配置已保存（备份：%1）").arg(backup);
    QMessageBox::information(this, QStringLiteral("已保存"), message);
    QTimer::singleShot(800, this, [this]() { reload(); });
}

void ConfigPage::openFile()
{
    if (!QFileInfo::exists(client_->configPath())) {
        QMessageBox::warning(this, QStringLiteral("配置文件不存在"), client_->configPath());
        return;
    }
    QDesktopServices::openUrl(QUrl::fromLocalFile(client_->configPath()));
}