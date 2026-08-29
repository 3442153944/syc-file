fn main() {
    // 嵌入 requireAdministrator 清单：应用启动时自动触发 UAC 提权。
    // 必须保留 Common-Controls v6 依赖，否则 tauri-plugin-dialog 相关 API 会失效。
    let windows = tauri_build::WindowsAttributes::new().app_manifest(
        r#"<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <dependency>
    <dependentAssembly>
      <assemblyIdentity
        type="win32"
        name="Microsoft.Windows.Common-Controls"
        version="6.0.0.0"
        processorArchitecture="*"
        publicKeyToken="6595b64144ccf1df"
        language="*"
      />
    </dependentAssembly>
  </dependency>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="requireAdministrator" uiAccess="false" />
      </requestedPrivileges>
    </security>
  </trustInfo>
</assembly>"#,
    );
    let attrs = tauri_build::Attributes::new().windows_attributes(windows);
    tauri_build::try_build(attrs).expect("failed to run build script");
}
