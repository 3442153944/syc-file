package file

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	gosync "sync"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/internal/sync"
	"syc-file/pkg/filecore"
	"syc-file/pkg/logger"
	"syc-file/pkg/token"
)

// 在线查看/编辑文本文件：
//
//	GET  /v1/file/text/read   读取（返回内容 + 版本号 hash）
//	POST /v1/file/text/save   保存（带上读到时的 hash 做乐观并发控制）
//
// 多人同时编辑的做法：每次保存都带上「我读到时的版本号」，服务端在锁内比对文件当前的版本号，
// 对不上说明期间有别人改过，不覆盖，把对方的最新内容连同版本号还给客户端，由客户端做三方合并后重新保存。
// 版本号就是整文件 blake3 哈希，和同步引擎里 trunk 记的 file_hash 是同一个值，
// 所以同步目录里的文件被改后其它设备会自动收到更新，也留有版本历史可回滚。

// textMaxBytes 在线编辑的文件大小上限：几 MB 的「文本」多半是日志或数据文件，放进编辑框只会卡死界面。
// 同时这个上限小于哈希分块大小，所以整文件哈希就等于对全部内容做一次 HashChunk（见 hashOfBytes）。
const textMaxBytes = 2 << 20

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// 可在线编辑的扩展名（小写，带点）。
var editableExts = map[string]bool{
	".txt": true, ".md": true, ".markdown": true, ".log": true, ".csv": true, ".tsv": true,
	".json": true, ".jsonc": true, ".yaml": true, ".yml": true, ".toml": true, ".ini": true, ".cfg": true,
	".conf": true, ".config": true, ".properties": true, ".env": true, ".xml": true, ".html": true, ".htm": true,
	".css": true, ".scss": true, ".js": true, ".mjs": true, ".ts": true, ".vue": true, ".go": true, ".py": true,
	".rs": true, ".java": true, ".kt": true, ".c": true, ".h": true, ".cpp": true, ".hpp": true, ".cs": true,
	".sh": true, ".bash": true, ".zsh": true, ".bat": true, ".cmd": true, ".ps1": true, ".sql": true,
	".gradle": true, ".lock": true, ".gitignore": true, ".editorconfig": true, ".service": true, ".rules": true,
}

// 没有扩展名或扩展名不标准、但确定是文本的常见文件名（小写比较）。
var editableNames = map[string]bool{
	"dockerfile": true, "makefile": true, "readme": true, "license": true, "hosts": true, "crontab": true,
	".env": true, ".gitignore": true, ".gitattributes": true, ".dockerignore": true, ".editorconfig": true,
	".bashrc": true, ".profile": true, ".zshrc": true, "fstab": true,
}

func isEditableText(name string) bool {
	lower := strings.ToLower(name)
	return editableExts[filepath.Ext(lower)] || editableNames[lower]
}

// textError 带业务码的错误，按项目惯例以 HTTP 200 + {code} 返回。
type textError struct {
	code int
	msg  string
}

func (e *textError) Error() string { return e.msg }

// textSnapshot 文件此刻的内容和版本。内容统一成 \n 换行、不含 BOM：
// 浏览器的文本框里换行本来就只有 \n，不统一的话编辑一个 CRLF 文件会把整份换行改掉，产生满屏无意义的差异。
// 换行风格和 BOM 单独记下，保存时还原。
type textSnapshot struct {
	Content string `json:"content"`
	Hash    string `json:"hash"`
	Size    int64  `json:"size"`
	MTimeMs int64  `json:"mtime_ms"`
	EOL     string `json:"eol"` // "lf" | "crlf"
	BOM     bool   `json:"bom"`

	mode os.FileMode
}

// hashOfBytes 整文件哈希（blake3）。内容不超过 textMaxBytes，不足一个分块，
// 此时整文件哈希就是对全部内容做一次 HashChunk；这个等式由测试与 filecore.Finalize 对拍保证。
// 直接对内存里的字节算，既不用落临时文件，也避免了「读内容」和「算哈希」之间文件被改的竞态。
func hashOfBytes(b []byte) (string, error) {
	h, err := filecore.HashChunk(b)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h), nil
}

func detectEOL(b []byte) string {
	crlf := bytes.Count(b, []byte("\r\n"))
	lf := bytes.Count(b, []byte("\n")) - crlf
	if crlf > 0 && crlf >= lf {
		return "crlf"
	}
	return "lf"
}

func readTextSnapshot(fullPath string) (*textSnapshot, *textError) {
	fi, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, &textError{404, "文件不存在"}
		}
		return nil, &textError{500, "读取文件信息失败"}
	}
	if !fi.Mode().IsRegular() {
		return nil, &textError{400, "不是普通文件"}
	}
	if fi.Size() > textMaxBytes {
		return nil, &textError{413, "文件超过 2MB，请下载后编辑"}
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, &textError{500, "读取文件失败"}
	}
	if len(data) > textMaxBytes { // stat 之后文件又变大了
		return nil, &textError{413, "文件超过 2MB，请下载后编辑"}
	}
	hash, err := hashOfBytes(data)
	if err != nil {
		return nil, &textError{500, "计算文件版本失败"}
	}

	body := data
	bom := bytes.HasPrefix(data, utf8BOM)
	if bom {
		body = data[len(utf8BOM):]
	}
	if bytes.IndexByte(body, 0) >= 0 {
		return nil, &textError{415, "看起来是二进制文件，不能作为文本编辑"}
	}
	if !utf8.Valid(body) {
		return nil, &textError{415, "文件不是 UTF-8 编码，不能在线编辑（避免保存后乱码）"}
	}
	eol := detectEOL(body)
	return &textSnapshot{
		Content: strings.ReplaceAll(string(body), "\r\n", "\n"),
		Hash:    hash,
		Size:    int64(len(data)),
		MTimeMs: fi.ModTime().UnixMilli(),
		EOL:     eol,
		BOM:     bom,
		mode:    fi.Mode().Perm(),
	}, nil
}

// encodeText 把编辑框里的内容还原成落盘字节：换行统一后按原文件风格转换，原来有 BOM 就补回去。
func encodeText(content, eol string, bom bool) []byte {
	c := strings.ReplaceAll(content, "\r\n", "\n")
	if eol == "crlf" {
		c = strings.ReplaceAll(c, "\n", "\r\n")
	}
	out := []byte(c)
	if bom {
		out = append(append([]byte{}, utf8BOM...), out...)
	}
	return out
}

// 按路径串行化「读-比对-写」：同一进程内两个保存请求不会交错。
var editLocks gosync.Map

func lockEdit(path string) func() {
	mu, _ := editLocks.LoadOrStore(path, &gosync.Mutex{})
	m := mu.(*gosync.Mutex)
	m.Lock()
	return m.Unlock
}

func textFullPath(path, name string) string {
	if filepath.Base(path) == name {
		return path
	}
	return filepath.Join(path, name)
}

func textFail(c *gin.Context, e *textError) {
	c.JSON(http.StatusOK, gin.H{"code": e.code, "message": e.msg, "data": nil})
}

// resolveTextTarget 校验登录、参数、扩展名、路径白名单，返回完整路径。失败时已写好响应。
func resolveTextTarget(c *gin.Context, path, name string) (string, *token.Claims, bool) {
	claimsAny, ok := c.Get("UserInfo")
	if !ok || claimsAny == nil {
		textFail(c, &textError{401, "请先登录"})
		return "", nil, false
	}
	claims, _ := claimsAny.(*token.Claims)
	if path == "" || name == "" {
		textFail(c, &textError{400, "缺少必要参数 path 或 name"})
		return "", nil, false
	}
	if !isEditableText(name) {
		textFail(c, &textError{415, "不支持在线查看/编辑该类型的文件"})
		return "", nil, false
	}
	full := textFullPath(path, name)
	if !isPathAllowedDownload(full) {
		textFail(c, &textError{403, "无权访问该路径"})
		return "", nil, false
	}
	return full, claims, true
}

// HandlerFuncTextRead GET /v1/file/text/read?path=&name=
func HandlerFuncTextRead(db *gorm.DB, redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		full, _, ok := resolveTextTarget(c, c.Query("path"), c.Query("name"))
		if !ok {
			return
		}
		snap, terr := readTextSnapshot(full)
		if terr != nil {
			textFail(c, terr)
			return
		}
		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": snap})
	}
}

type textSaveReq struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	BaseHash string `json:"base_hash"` // 编辑时读到的版本号
	Force    bool   `json:"force"`     // 用户明确选择覆盖他人修改
}

// HandlerFuncTextSave POST /v1/file/text/save
//
// 成功：data = {saved:true, hash, size, mtime_ms, synced, unchanged}
// 版本冲突（别人在此期间改过）：不是错误，data = {saved:false, conflict:true, current:{...}}，
// current 带对方最新内容和版本号，客户端据此合并后用新版本号重新保存。
func HandlerFuncTextSave(db *gorm.DB, redisClient *redis.Client, engine *sync.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req textSaveReq
		if err := c.ShouldBindJSON(&req); err != nil {
			textFail(c, &textError{400, "参数错误"})
			return
		}
		full, claims, ok := resolveTextTarget(c, req.Path, req.Name)
		if !ok {
			return
		}
		if req.BaseHash == "" && !req.Force {
			textFail(c, &textError{400, "缺少 base_hash"})
			return
		}
		if len(req.Content) > textMaxBytes {
			textFail(c, &textError{413, "内容超过 2MB"})
			return
		}

		unlock := lockEdit(full)
		defer unlock()

		cur, terr := readTextSnapshot(full)
		if terr != nil {
			textFail(c, terr)
			return
		}
		if !req.Force && req.BaseHash != cur.Hash {
			c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": gin.H{"saved": false, "conflict": true, "current": cur}})
			return
		}

		raw := encodeText(req.Content, cur.EOL, cur.BOM)
		if len(raw) > textMaxBytes {
			textFail(c, &textError{413, "内容超过 2MB"})
			return
		}
		newHash, err := hashOfBytes(raw)
		if err != nil {
			textFail(c, &textError{500, "计算文件版本失败"})
			return
		}
		if newHash == cur.Hash { // 内容没变：不写盘、不升版本、不派发
			c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": gin.H{
				"saved": true, "unchanged": true, "hash": cur.Hash, "size": cur.Size, "mtime_ms": cur.MTimeMs,
			}})
			return
		}

		if err := writeFileAtomic(full, raw, cur.mode); err != nil {
			logger.Logger.Error("保存文本文件失败", zap.String("path", full), zap.Error(err))
			textFail(c, &textError{500, "保存失败"})
			return
		}
		fi, _ := os.Stat(full)
		var mtimeMs int64
		if fi != nil {
			mtimeMs = fi.ModTime().UnixMilli()
		}

		synced := registerSavedFile(db, engine, uint(claims.UserID), full, req.Name, int64(len(raw)), newHash)
		logger.Logger.Info("文本文件已保存", zap.Int64("user_id", claims.UserID), zap.String("path", full),
			zap.Int("size", len(raw)), zap.Bool("force", req.Force), zap.Bool("synced", synced))

		c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": gin.H{
			"saved": true, "hash": newHash, "size": len(raw), "mtime_ms": mtimeMs, "synced": synced,
		}})
	}
}

// textTempPath 保存时的临时文件路径；做成变量只是为了单元测试能把它指到测试目录里，不去碰存储盘根目录。
var textTempPath = tempPathFor

// writeFileAtomic 先写到同盘的临时目录再原子改名：读取方（包括同步下载）永远看不到写了一半的文件。
// 临时文件复用上传用的 <盘>/<base_path>/<temp_path>/*.part，崩溃遗留的由临时目录清理器兜底。
func writeFileAtomic(target string, data []byte, mode os.FileMode) error {
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	tmp := textTempPath(target, "edit-"+hex.EncodeToString(rnd[:]))
	if err := os.MkdirAll(filepath.Dir(tmp), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil { // 保留原文件权限
		_ = os.Remove(tmp)
		return err
	}
	if err := filecore.Move(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// registerSavedFile 让文件的新内容进入文件库：目标在同步文件夹内就交给同步引擎（更新 trunk、记版本历史、
// 向各设备派发拉取）；否则普通地更新 file 表。来源记为服务端自己而不是某台设备——
// 这样派发时不会漏掉任何设备，包括正在编辑的这台自己的本地同步副本。返回是否走了同步引擎。
// db 为 nil 时什么都不做（只给不带数据库的单元测试用）。
func registerSavedFile(db *gorm.DB, engine *sync.Engine, userID uint, fullPath, name string, size int64, hash string) bool {
	if db == nil {
		return false
	}
	if engine != nil {
		handled, err := engine.HandleUploadComplete(userID, sync.SourceServer, fullPath, name, size, hash)
		if err != nil {
			logger.Logger.Warn("文本保存后的同步派发失败", zap.String("path", fullPath), zap.Error(err))
		}
		if handled {
			return true
		}
	}
	if _, err := upsertFileRecord(db, userID, fullPath, name, size, hash); err != nil {
		logger.Logger.Warn("文本保存后更新文件记录失败", zap.String("path", fullPath), zap.Error(err))
	}
	return false
}
