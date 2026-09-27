// commands/credential.rs
// 职责：记住密码——OS 原生凭据管理器。
//
// 不用自己拿对称密钥加密再存本地文件——密钥和密文摆在同一台机器上，密钥藏哪都等于
// 没加密，纯自研方案在“不用二次输入就能自动解密”这个前提下必然是假加密。真正站得住
// 脚的是操作系统自己的凭据管理器：Windows Credential Manager（DPAPI，绑定当前系统账户）、
// macOS Keychain、Linux Secret Service（多数发行版的桌面环境自带，没有的话下面会优雅降级
// 成“记不住密码”而不是崩溃）。userName 只是索引键，不敏感，继续放 localStorage 就行。
const CREDENTIAL_SERVICE: &str = "com.sunyuanling.filesync-desktop";

/// 记住密码：保存到 OS 凭据管理器。
#[tauri::command]
pub fn save_remembered_credential(username: String, password: String) -> Result<(), String> {
    let entry = keyring::Entry::new(CREDENTIAL_SERVICE, &username).map_err(|e| e.to_string())?;
    entry.set_password(&password).map_err(|e| e.to_string())?;
    Ok(())
}

/// 取记住的密码：没记住过（或者已被清掉）时返回 `None`，不是错误。
#[tauri::command]
pub fn get_remembered_credential(username: String) -> Result<Option<String>, String> {
    let entry = keyring::Entry::new(CREDENTIAL_SERVICE, &username).map_err(|e| e.to_string())?;
    match entry.get_password() {
        Ok(password) => Ok(Some(password)),
        Err(keyring::Error::NoEntry) => Ok(None),
        Err(e) => Err(e.to_string()),
    }
}

/// 清掉记住的密码（取消勾选"记住密码"、或者换记别的账号时用来清旧的）。
/// 本来就没有也不算错误，幂等。
#[tauri::command]
pub fn clear_remembered_credential(username: String) -> Result<(), String> {
    let entry = keyring::Entry::new(CREDENTIAL_SERVICE, &username).map_err(|e| e.to_string())?;
    match entry.delete_credential() {
        Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
        Err(e) => Err(e.to_string()),
    }
}
