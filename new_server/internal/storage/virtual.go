package storage

import (
	"bytes"
	"encoding/json"
	"gorm.io/gorm"
	"path/filepath"
	"strings"
)

const VirtualRoot = "/@space"

func internalPath(rel string) bool {
	// Windows 文件系统不区分大小写，大小写变体也必须视为内部目录。
	part := strings.ToLower(strings.Split(filepath.ToSlash(rel), "/")[0])
	return part == ".uploads" || part == ".children" || part == ".versions"
}

// ResolveVirtual 将当前账号的虚拟路径解析为服务器目录，不接受跨级或内部保留路径。
func ResolveVirtual(db *gorm.DB, uid uint, path string) (string, error) {
	normal := filepath.ToSlash(path)
	if normal != VirtualRoot && !strings.HasPrefix(normal, VirtualRoot+"/") {
		return path, nil
	}
	s, p, err := Load(db, uid)
	if err != nil {
		return "", err
	}
	rel := strings.TrimPrefix(normal, VirtualRoot)
	for _, part := range strings.Split(rel, "/") {
		if part == ".." || part == "." || strings.Contains(part, ":") {
			return "", ErrDenied
		}
	}
	rel = strings.TrimPrefix(rel, "/")
	if internalPath(rel) {
		return "", ErrDenied
	}
	root := Root(p, s)
	full := filepath.Join(root, filepath.FromSlash(rel))
	if !Within(root, full) {
		return "", ErrDenied
	}
	return full, nil
}

// ClientData 隐藏非系统管理员响应中的实际路径，磁盘监控也不返回系统分区。
// 使用 JSON Number 保留大整数，避免路径脱敏时损失容量或文件标识的精度。
func ClientData(root string, data any) any {
	b, err := json.Marshal(data)
	if err != nil {
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return nil
	}
	pathKeys := map[string]bool{"default_path": true, "path": true, "file_path": true, "storage_path": true, "remote_path": true, "root_path": true, "parent_path": true, "original_path": true, "current_path": true, "mountpoint": true}
	var clean func(any, string) any
	clean = func(v any, key string) any {
		switch x := v.(type) {
		case map[string]any:
			for k, val := range x {
				if k == "disks" {
					x[k] = []any{}
				} else {
					x[k] = clean(val, k)
				}
			}
			return x
		case []any:
			for i, val := range x {
				x[i] = clean(val, key)
			}
			return x
		case string:
			if root != "" {
				x = strings.ReplaceAll(x, root, VirtualRoot)
				x = strings.ReplaceAll(x, filepath.ToSlash(root), VirtualRoot)
			}
			if pathKeys[key] {
				if strings.HasPrefix(x, VirtualRoot) {
					return strings.ReplaceAll(x, "\\", "/")
				}
				if filepath.IsAbs(x) || strings.HasPrefix(x, "/") || strings.Contains(x, ":\\") {
					return ""
				}
			}
			return x
		default:
			return v
		}
	}
	return clean(value, "")
}
