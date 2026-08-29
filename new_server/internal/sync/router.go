package sync

import "github.com/gin-gonic/gin"

func RegisterSyncRouter(rg *gin.RouterGroup, engine *Engine) {
	h := NewAPIHandler(engine)
	s := rg.Group("/sync")
	s.POST("/folder", h.SaveFolder)
	s.GET("/folder", h.GetFolder)
	s.PUT("/folder", h.UpdateFolder)
	s.DELETE("/folder", h.DeleteFolder)
	s.POST("/notify", h.Notify)
	s.POST("/scan", h.Scan)
	s.GET("/tasks", h.ListTasks)
	s.DELETE("/tasks", h.ClearTasks)
	s.DELETE("/tasks/:id", h.DeleteTask)
	s.GET("/tasks/pending", h.PendingTasks)
	s.POST("/tasks/:id/complete", h.CompleteTask)
	s.POST("/tasks/:id/failed", h.FailTask)
	s.POST("/tasks/:id/blocked", h.BlockTask)
	s.GET("/conflicts", h.ListConflicts)
	s.POST("/conflicts/:id/resolve", h.ResolveConflict)
	s.DELETE("/conflicts/:id", h.DeleteConflict)
}
