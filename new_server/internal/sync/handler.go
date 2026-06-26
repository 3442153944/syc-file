package sync

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"syc-file/internal/model"
	"syc-file/pkg/token"
)

type APIHandler struct {
	engine *Engine
}

func NewAPIHandler(engine *Engine) *APIHandler {
	return &APIHandler{engine: engine}
}

func (h *APIHandler) CreateFolder(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	var req struct {
		Name          string `json:"name"`
		LocalPath     string `json:"local_path" binding:"required"`
		RemotePath    string `json:"remote_path" binding:"required"`
		Direction     string `json:"direction"`
		OwnerDeviceID string `json:"owner_device_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "参数解析失败")
		return
	}
	f, err := h.engine.CreateFolder(userID, req.OwnerDeviceID, req.Name, req.LocalPath, req.RemotePath, req.Direction)
	if err != nil {
		jsonErr(c, 400, err.Error())
		return
	}
	jsonOK(c, f)
}

func (h *APIHandler) ListFolders(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	fs, err := h.engine.ListFolders(userID)
	if err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	jsonOK(c, fs)
}

func (h *APIHandler) UpdateFolder(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		jsonErr(c, 400, "无效的文件夹ID")
		return
	}
	var updates map[string]interface{}
	if err := c.ShouldBindJSON(&updates); err != nil {
		jsonErr(c, 400, "参数解析失败")
		return
	}
	if err := h.engine.UpdateFolder(userID, id, updates); err != nil {
		jsonErr(c, 400, err.Error())
		return
	}
	jsonOK(c, nil)
}

func (h *APIHandler) DeleteFolder(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		jsonErr(c, 400, "无效的文件夹ID")
		return
	}
	if err := h.engine.DeleteFolder(userID, id); err != nil {
		jsonErr(c, 400, err.Error())
		return
	}
	jsonOK(c, nil)
}

func (h *APIHandler) Notify(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	var req struct {
		DeviceID string `json:"device_id" binding:"required"`
		FileChangeReport
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "参数解析失败")
		return
	}
	if err := h.engine.HandleFileChange(userID, req.DeviceID, req.FileChangeReport); err != nil {
		jsonErr(c, 400, err.Error())
		return
	}
	jsonOK(c, nil)
}

func (h *APIHandler) Scan(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	var req struct {
		DeviceID string     `json:"device_id" binding:"required"`
		FolderID uint64     `json:"folder_id" binding:"required"`
		Items    []ScanItem `json:"items"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonErr(c, 400, "参数解析失败")
		return
	}
	if err := h.engine.HandleScan(userID, req.DeviceID, ScanReport{FolderID: req.FolderID, Items: req.Items}); err != nil {
		jsonErr(c, 400, err.Error())
		return
	}
	jsonOK(c, nil)
}

func (h *APIHandler) ListTasks(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	status := c.Query("status")
	deviceID := c.Query("device_id")
	limit, _ := strconv.Atoi(c.Query("limit"))
	ts, err := h.engine.ListTasks(userID, status, deviceID, limit)
	if err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	jsonOK(c, ts)
}

func (h *APIHandler) PendingTasks(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	deviceID := c.Query("device_id")
	if deviceID == "" {
		jsonErr(c, 400, "缺少 device_id")
		return
	}
	ts, err := h.engine.PendingTasksForDevice(userID, deviceID)
	if err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	jsonOK(c, ts)
}

func (h *APIHandler) CompleteTask(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		jsonErr(c, 400, "无效的任务ID")
		return
	}
	var req struct {
		FileHash string `json:"file_hash"`
	}
	_ = c.ShouldBindJSON(&req)
	if !h.engineOwnsTask(userID, id) {
		jsonErr(c, 403, "无权操作该任务")
		return
	}
	h.engine.CompleteTask(id, req.FileHash)
	jsonOK(c, nil)
}

func (h *APIHandler) FailTask(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		jsonErr(c, 400, "无效的任务ID")
		return
	}
	var req struct {
		Error string `json:"error"`
	}
	_ = c.ShouldBindJSON(&req)
	if !h.engineOwnsTask(userID, id) {
		jsonErr(c, 403, "无权操作该任务")
		return
	}
	h.engine.FailTask(id, req.Error)
	jsonOK(c, nil)
}

func (h *APIHandler) ListConflicts(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	ts, err := h.engine.ListConflicts(userID)
	if err != nil {
		jsonErr(c, 500, err.Error())
		return
	}
	jsonOK(c, ts)
}

func (h *APIHandler) ResolveConflict(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		jsonErr(c, 400, "无效的记录ID")
		return
	}
	if err := h.engine.ResolveConflict(userID, id); err != nil {
		jsonErr(c, 400, err.Error())
		return
	}
	jsonOK(c, nil)
}

func (h *APIHandler) engineOwnsTask(userID uint, taskID uint64) bool {
	var t model.SyncTask
	if err := h.engine.db.Select("user_id").Where("id = ?", taskID).First(&t).Error; err != nil {
		return false
	}
	return t.UserID == userID
}

func requireUser(c *gin.Context) (uint, bool) {
	claimsAny, exists := c.Get("UserInfo")
	if !exists || claimsAny == nil {
		c.JSON(http.StatusOK, gin.H{"code": 401, "message": "未授权", "data": nil})
		return 0, false
	}
	userClaims := claimsAny.(*token.Claims)
	return uint(userClaims.UserID), true
}

func jsonOK(c *gin.Context, data interface{}) {
	c.JSON(http.StatusOK, gin.H{"code": 200, "message": "ok", "data": data})
}

func jsonErr(c *gin.Context, code int, msg string) {
	c.JSON(http.StatusOK, gin.H{"code": code, "message": msg, "data": nil})
}
