package file

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"syc-file/internal/storage"
	"syc-file/pkg/response"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/internal/model"
	syncpkg "syc-file/internal/sync"
	"syc-file/pkg/logger"
	"syc-file/pkg/token"
)

// 文件版本历史：查询 / 下载某版本 / 回滚。
//
// 版本内容存在内容寻址的版本仓库里（见 internal/sync/version.go）。
// ⚠ **只有本功能上线之后产生的版本才有内容**：在此之前 file_version 的 storage_path 一直是 NULL，
// 那些历史版本只能看元数据、不能下载/回滚（接口会明确报错，不会给个假成功）。

// versionRow 版本行 + 是否可回滚（内容还在不在）。
type versionRow struct {
	model.FileVersion
	CreatorName string `json:"creator_name"`
	// Restorable 版本内容是否还在仓库里。false = 只有元数据，不能下载/回滚
	Restorable bool `json:"restorable"`
	IsCurrent  bool `json:"is_current"`
}

// HandlerFuncFileVersions POST /v1/file/versions —— 按路径或 file_id 查版本历史。
// body: {"path":"E:\\FileSync\\a.txt"} 或 {"file_id":123}
func HandlerFuncFileVersions(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := currentUserID(c)
		if !ok {
			return
		}
		var req struct {
			Path   string `json:"path"`
			FileID uint64 `json:"file_id"`
		}
		_ = c.ShouldBindJSON(&req)

		file, ok := lookupFile(c, db, userID, req.FileID, req.Path)
		if !ok {
			return
		}

		var versions []model.FileVersion
		if err := db.Where("file_id = ?", file.ID).Order("version desc").Find(&versions).Error; err != nil {
			response.WriteHTTP(c, http.StatusOK, 500, err.Error(), nil)
			return
		}

		names := creatorNames(db, versions)
		rows := make([]versionRow, 0, len(versions))
		for _, v := range versions {
			row := versionRow{FileVersion: v, IsCurrent: uint(v.Version) == file.Version}
			if v.CreatedBy != nil {
				row.CreatorName = names[*v.CreatedBy]
			}
			if v.StoragePath != nil && *v.StoragePath != "" {
				if _, err := os.Stat(*v.StoragePath); err == nil {
					row.Restorable = true
				}
			}
			rows = append(rows, row)
		}
		response.WriteHTTP(c, http.StatusOK, 200, "ok", gin.H{
			"file":     file,
			"versions": rows,
		})
	}
}

// HandlerFuncDownloadVersion GET /v1/file/version/download?version_id=123 —— 下载某个历史版本。
func HandlerFuncDownloadVersion(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := currentUserID(c)
		if !ok {
			return
		}
		vid, err := strconv.ParseUint(c.Query("version_id"), 10, 64)
		if err != nil || vid == 0 {
			response.WriteHTTP(c, http.StatusBadRequest, 400, "无效的 version_id", nil)
			return
		}
		v, file, ok := lookupVersion(c, db, userID, vid)
		if !ok {
			return
		}
		if v.StoragePath == nil || *v.StoragePath == "" {
			response.WriteHTTP(c, http.StatusNotFound, 404, "该版本没有留存内容（早于版本仓库上线）", nil)
			return
		}
		if _, err := os.Stat(*v.StoragePath); err != nil {
			response.WriteHTTP(c, http.StatusNotFound, 404, "版本内容已丢失", nil)
			return
		}
		// 下载名带上版本号，避免和当前版本混淆
		ext := filepath.Ext(file.FileName)
		base := file.FileName[:len(file.FileName)-len(ext)]
		c.FileAttachment(*v.StoragePath, base+"_v"+strconv.Itoa(v.Version)+ext)
	}
}

// HandlerFuncRollbackVersion POST /v1/file/version/rollback —— 把文件回滚到某个历史版本。
// body: {"version_id":123, "device_id":"..."}
//
// 实现方式是「把旧内容当成一次新的修改提交」：复制 blob 回目标路径 → 交同步引擎按
// 正常的 file_changed 流程处理。于是版本号继续递增（不是倒退）、其它设备照常收到 download 任务，
// 与手工改一次文件在语义上完全一致。历史因此是**只增不改**的，回滚本身也留下一条记录。
func HandlerFuncRollbackVersion(db *gorm.DB, engine *syncpkg.Engine) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, ok := currentUserID(c)
		if !ok {
			return
		}
		var req struct {
			VersionID uint64 `json:"version_id"`
			DeviceID  string `json:"device_id"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			response.WriteHTTP(c, http.StatusOK, 400, "参数解析失败: "+err.Error(), nil)
			return
		}
		v, file, ok := lookupVersion(c, db, userID, req.VersionID)
		if !ok {
			return
		}
		if v.StoragePath == nil || *v.StoragePath == "" {
			response.WriteHTTP(c, http.StatusOK, 400, "该版本没有留存内容，无法回滚", nil)
			return
		}
		if _, err := os.Stat(*v.StoragePath); err != nil {
			response.WriteHTTP(c, http.StatusOK, 400, "版本内容已丢失，无法回滚", nil)
			return
		}
		if uint(v.Version) == file.Version {
			response.WriteHTTP(c, http.StatusOK, 400, "该版本已是当前版本", nil)
			return
		}
		if engine == nil {
			response.WriteHTTP(c, http.StatusOK, 500, "同步引擎未就绪", nil)
			return
		}

		// 找出这个文件属于哪个同步 folder：回滚要走同步引擎才能派发给其它设备
		folder, rel, inSync := engine.FolderForPath(userID, file.FilePath)
		if !inSync {
			response.WriteHTTP(c, http.StatusOK, 400, "该文件不在任何同步文件夹内，暂不支持回滚", nil)
			return
		}

		// 覆盖目标文件：先写同目录的 .rollback 临时文件再原子改名，中途失败不会毁掉现有文件
		blobInfo, err := os.Stat(*v.StoragePath)
		if err != nil {
			response.Fail(c, 404, "版本内容不存在")
			return
		}
		err = storage.Allocate(db, userID, file.FilePath, blobInfo.Size(), func() error {
			temp, err := os.CreateTemp(filepath.Dir(file.FilePath), ".rollback-*.part")
			if err != nil {
				return err
			}
			tmp := temp.Name()
			temp.Close()
			defer os.Remove(tmp)
			if err := copyFile(*v.StoragePath, tmp); err != nil {
				return err
			}
			return os.Rename(tmp, file.FilePath)
		})
		if err != nil {
			response.Fail(c, 400, err.Error())
			return
		}

		size := int64(0)
		if v.FileSize != nil {
			size = *v.FileSize
		}
		hash := ""
		if v.FileHash != nil {
			hash = *v.FileHash
		}
		report := syncpkg.FileChangeReport{
			FolderID:     folder.ID,
			RelativePath: rel,
			FileName:     file.FileName,
			Action:       model.FileChangeModify,
			FileSize:     size,
			FileHash:     hash,
			// base 用 trunk 当前哈希：这就是一次正常的「基于最新版本的修改」，不会被判成冲突
			BaseHash: derefStrValue(file.FileHash),
		}
		if err := engine.HandleFileChange(userID, req.DeviceID, report); err != nil {
			response.WriteHTTP(c, http.StatusOK, 500, "回滚已落盘但同步派发失败: "+err.Error(), nil)
			return
		}
		logger.Logger.Info("文件已回滚到历史版本",
			zap.String("path", file.FilePath), zap.Int("to_version", v.Version))
		response.WriteHTTP(c, http.StatusOK, 200, "ok", gin.H{
			"restored_version": v.Version,
			"file_hash":        hash,
		})
	}
}

// ---- helpers ----

func currentUserID(c *gin.Context) (uint, bool) {
	claims, ok := c.Get("UserInfo")
	if !ok || claims == nil {
		response.WriteHTTP(c, http.StatusOK, 401, "请先登录", nil)
		return 0, false
	}
	return uint(claims.(*token.Claims).UserID), true
}

// lookupFile 按 file_id 或路径取文件行，并校验归属。
func lookupFile(c *gin.Context, db *gorm.DB, userID uint, fileID uint64, path string) (model.File, bool) {
	var file model.File
	q := db.Where("user_id = ?", userID)
	switch {
	case fileID > 0:
		q = q.Where("id = ?", fileID)
	case path != "":
		q = q.Where("file_path = ?", filepath.Clean(path))
	default:
		response.WriteHTTP(c, http.StatusOK, 400, "需要 path 或 file_id", nil)
		return file, false
	}
	if err := q.First(&file).Error; err != nil {
		response.WriteHTTP(c, http.StatusOK, 404, "文件记录不存在", nil)
		return file, false
	}
	if !checkFilePath(c, db, file.FilePath) {
		return file, false
	}
	return file, true
}

// lookupVersion 取版本行并校验它属于当前用户的文件。
func lookupVersion(c *gin.Context, db *gorm.DB, userID uint, versionID uint64) (model.FileVersion, model.File, bool) {
	var v model.FileVersion
	if err := db.First(&v, versionID).Error; err != nil {
		response.WriteHTTP(c, http.StatusOK, 404, "版本不存在", nil)
		return v, model.File{}, false
	}
	var file model.File
	if err := db.Where("id = ? AND user_id = ?", v.FileID, userID).First(&file).Error; err != nil {
		response.WriteHTTP(c, http.StatusOK, 403, "无权访问该版本", nil)
		return v, file, false
	}
	if !checkFilePath(c, db, file.FilePath) {
		return v, file, false
	}
	return v, file, true
}

func creatorNames(db *gorm.DB, versions []model.FileVersion) map[uint]string {
	ids := make([]uint, 0, len(versions))
	for _, v := range versions {
		if v.CreatedBy != nil {
			ids = append(ids, *v.CreatedBy)
		}
	}
	names := map[uint]string{}
	if len(ids) == 0 {
		return names
	}
	var users []model.User
	db.Select("id, username").Where("id IN ?", ids).Find(&users)
	for _, u := range users {
		names[u.ID] = u.Username
	}
	return names
}

func derefStrValue(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
