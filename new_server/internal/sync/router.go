package sync

import "github.com/gin-gonic/gin"

func RegisterSyncRouter(rg *gin.RouterGroup, engine *Engine) {
	h := NewAPIHandler(engine)
	s := rg.Group("/sync")
	s.POST("/folders", h.CreateFolder)
	s.GET("/folders", h.ListFolders)
	s.PUT("/folders/:id", h.UpdateFolder)
	s.DELETE("/folders/:id", h.DeleteFolder)
	s.POST("/notify", h.Notify)
	s.POST("/scan", h.Scan)
	s.GET("/tasks", h.ListTasks)
	s.GET("/tasks/pending", h.PendingTasks)
	s.POST("/tasks/:id/complete", h.CompleteTask)
	s.POST("/tasks/:id/failed", h.FailTask)
	s.GET("/conflicts", h.ListConflicts)
	s.DELETE("/conflicts/:id", h.ResolveConflict)
}
