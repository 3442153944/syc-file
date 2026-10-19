package file

import (
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"net/http"
	"path/filepath"
	"strings"
	"syc-file/internal/storage"
	"syc-file/pkg/response"
	"syc-file/pkg/token"
	"syc-file/pkg/upload_store"
)

// resolveFilePath 在接触文件系统之前解析虚拟路径，再检查归属和有效期。
func resolveFilePath(c *gin.Context, db *gorm.DB, path string) (string, bool) {
	value, ok := c.Get("UserInfo")
	if !ok || value == nil {
		response.Fail(c, 401, "请先登录")
		return "", false
	}
	claims, ok := value.(*token.Claims)
	if !ok {
		response.Fail(c, 401, "请先登录")
		return "", false
	}
	full := path
	if db != nil {
		var err error
		full, err = storage.ResolveVirtual(db, uint(claims.UserID), path)
		if err != nil {
			fileAccessDenied(c, err.Error())
			return "", false
		}
	}
	if !checkFilePath(c, db, full) {
		return "", false
	}
	return full, true
}

// checkFilePath 在每个实际读写入口校验当前账号、目录归属及有效期。
// 文件流端点使用真实 HTTP 错误状态，防止客户端把错误 JSON 保存为文件。
func checkFilePath(c *gin.Context, db *gorm.DB, path string) bool {
	claims, ok := c.Get("UserInfo")
	if !ok || claims == nil {
		response.WriteHTTP(c, 401, 401, "请先登录", nil)
		return false
	}
	user, ok := claims.(*token.Claims)
	if !ok {
		response.WriteHTTP(c, 401, 401, "请先登录", nil)
		return false
	}
	if err := storage.Authorize(db, uint(user.UserID), path); err != nil {
		fileAccessDenied(c, err.Error())
		return false
	}
	return true
}

func fileAccessDenied(c *gin.Context, message string) {
	status := http.StatusOK
	if c.Request.Method == http.MethodGet && (strings.HasSuffix(c.Request.URL.Path, "/download") || strings.HasSuffix(c.Request.URL.Path, "/thumbnail")) {
		status = http.StatusForbidden
	}
	response.WriteHTTP(c, status, 403, message, nil)
}

// checkUploadStage 拒绝升级前位于空间外的旧暂存文件，要求客户端重新初始化。
func checkUploadStage(c *gin.Context, db *gorm.DB, sess *upload_store.Session) bool {
	if sess.QuickShare {
		return true
	}
	if expected, managed := storage.Temp(db, sess.UserID, sess.TargetPath, sess.ID); managed && filepath.Clean(expected) != filepath.Clean(sess.TempPath) {
		response.Fail(c, 409, "上传空间已调整，请重新初始化上传")
		return false
	}
	return true
}

// allocateUpload 保留快传既有配额；普通文件在用户空间内预占容量。
func allocateUpload(db *gorm.DB, uid uint, path string, size int64, quick bool, write func() error) error {
	if quick {
		return write()
	}
	return storage.Allocate(db, uid, path, size, write)
}
