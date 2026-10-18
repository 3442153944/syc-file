package file

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"syc-file/internal/storage"
	"syc-file/pkg/response"
	"syc-file/pkg/token"
)

// validDirectoryName 只接受单个、可跨 Windows/Linux 使用的目录名，不接受路径或内部保留目录。
func validDirectoryName(name string) bool {
	if name == "" || len(name) > 255 || strings.TrimSpace(name) != name || strings.HasSuffix(name, ".") || strings.ContainsAny(name, "/\\:*?\"<>|") {
		return false
	}
	lower := strings.ToLower(name)
	if name == "." || name == ".." || lower == ".uploads" || lower == ".children" || lower == ".versions" {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" {
		return false
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return false
	}
	return true
}

// HandlerFuncCreateDirectory 复用虚拟路径、实时权限及空间锁；同名目录返回冲突，绝不覆盖。
func HandlerFuncCreateDirectory(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req struct {
			Path string `json:"path" binding:"required"`
			Name string `json:"name" binding:"required"`
		}
		if c.ShouldBindJSON(&req) != nil || !validDirectoryName(req.Name) {
			response.Fail(c, 400, "文件夹名称无效，不能包含路径分隔符、特殊字符或保留名称")
			return
		}
		parent, ok := resolveFilePath(c, db, req.Path)
		if !ok {
			return
		}
		info, err := os.Stat(parent)
		if err != nil || !info.IsDir() {
			response.Fail(c, 404, "父目录不存在或不可访问")
			return
		}
		target := filepath.Join(parent, req.Name)
		if !storage.Within(parent, target) {
			response.Fail(c, 403, "无权在该位置创建文件夹")
			return
		}
		if !checkFilePath(c, db, target) {
			return
		}
		if _, err := os.Lstat(target); err == nil {
			response.Fail(c, 409, "同名文件或文件夹已存在")
			return
		} else if !os.IsNotExist(err) {
			response.Fail(c, 500, "无法检查目标目录")
			return
		}
		value, _ := c.Get("UserInfo")
		uid := uint(value.(*token.Claims).UserID)
		err = storage.Allocate(db, uid, target, 0, func() error { return os.Mkdir(target, 0700) })
		if err != nil {
			switch {
			case os.IsExist(err):
				response.Fail(c, 409, "同名文件或文件夹已存在")
			case errors.Is(err, storage.ErrDenied), errors.Is(err, storage.ErrFrozen):
				response.Fail(c, 403, err.Error())
			case errors.Is(err, storage.ErrQuota):
				response.Fail(c, 409, err.Error())
			default:
				response.Fail(c, 500, "创建文件夹失败，请检查目录权限和磁盘状态")
			}
			return
		}
		response.OK(c, gin.H{"path": target, "name": req.Name})
	}
}
