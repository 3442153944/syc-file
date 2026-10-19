package file

import (
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/shirou/gopsutil/v3/disk"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"syc-file/config"
	"syc-file/internal/model"
	"syc-file/internal/storage"
	"syc-file/pkg/logger"
	"syc-file/pkg/response"
	"syc-file/pkg/token"
)

type diskInfoBrief struct {
	Path         string  `json:"path"`
	Mountpoint   string  `json:"mountpoint"`
	Device       string  `json:"device"`
	Fstype       string  `json:"fstype"`
	Total        uint64  `json:"total"`
	Free         uint64  `json:"free"`
	Used         uint64  `json:"used"`
	UsedPercent  float64 `json:"used_percent"`
	TotalGB      string  `json:"total_gb"`
	FreeGB       string  `json:"free_gb"`
	IsAllowed    bool    `json:"is_allowed"`
	IsAccessible bool    `json:"is_accessible"`
	IsSSD        bool    `json:"is_ssd"`
}

func HandlerFuncAvailableDisks(db *gorm.DB, redisClient *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, exists := c.Get("UserInfo")
		if !exists || claims == nil {
			response.Fail(c, 401, "请先登录")
			return
		}
		uid := uint(claims.(*token.Claims).UserID)
		u, err := storage.User(db, uid)
		if err != nil {
			response.Fail(c, 403, "账号不可用")
			return
		}
		if u.Level < model.LevelSuper {
			space, err := storage.Mine(db, uid)
			if err != nil || space.Frozen {
				response.Fail(c, 403, "上传空间尚未分配、已到期或不可用，请联系系统管理员")
				return
			}
			quota := uint64(space.QuotaBytes)
			used := uint64(space.UsedBytes)
			free := uint64(0)
			if quota > used {
				free = quota - used
			}
			d := diskInfoBrief{Path: space.Path, Mountpoint: space.Path, Device: space.PoolName, Total: quota, Used: used, Free: free, IsAllowed: true, IsAccessible: true}
			d.TotalGB = formatBytesForDisk(quota)
			d.FreeGB = formatBytesForDisk(free)
			if quota > 0 {
				d.UsedPercent = float64(used) / float64(quota) * 100
			}
			response.OK(c, gin.H{"total": 1, "allowed_count": 1, "allowed_disks": []diskInfoBrief{d}, "all_disks": []diskInfoBrief{d}})
			return
		}
		var req struct {
			DiskPath string `json:"disk_path"`
			Detailed bool   `json:"detailed"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			req = struct {
				DiskPath string `json:"disk_path"`
				Detailed bool   `json:"detailed"`
			}{}
		}

		if req.DiskPath != "" {
			detail, err := getDetailedDiskInfo(req.DiskPath)
			if err != nil {
				logger.Logger.Error("获取磁盘详细信息失败", zap.String("disk", req.DiskPath), zap.Error(err))
				response.WriteHTTP(c, http.StatusOK, 500, err.Error(), nil)
				return
			}
			response.WriteHTTP(c, http.StatusOK, 200, "ok", detail)
			return
		}

		disks, err := getBriefDiskList()
		if err != nil {
			logger.Logger.Error("获取磁盘列表失败", zap.Error(err))
			response.WriteHTTP(c, http.StatusOK, 500, "获取磁盘信息失败", nil)
			return
		}

		var allowedDisks []diskInfoBrief
		var allDisks []diskInfoBrief
		for _, d := range disks {
			allDisks = append(allDisks, d)
			if d.IsAllowed && d.IsAccessible {
				allowedDisks = append(allowedDisks, d)
			}
		}

		response.WriteHTTP(c, http.StatusOK, 200, "ok", gin.H{
			"total":         len(allDisks),
			"allowed_count": len(allowedDisks),
			"allowed_disks": allowedDisks,
			"all_disks":     allDisks,
		})
	}
}

func getBriefDiskList() ([]diskInfoBrief, error) {
	partitions, err := disk.Partitions(false)
	if err != nil {
		return nil, fmt.Errorf("获取磁盘分区失败: %w", err)
	}

	allowedPaths := config.Conf.GetAllowedPaths()
	var disks []diskInfoBrief

	for _, partition := range partitions {
		if shouldSkipPartition(partition) {
			continue
		}
		usage, err := disk.Usage(partition.Mountpoint)
		if err != nil {
			continue
		}
		diskPath := normalizeDiskPath(partition.Mountpoint)
		isAllowed := isPathInList(diskPath, allowedPaths)
		isAccessible := isDiskAccessible(partition.Mountpoint)

		disks = append(disks, diskInfoBrief{
			Path:         diskPath,
			Mountpoint:   partition.Mountpoint,
			Device:       partition.Device,
			Fstype:       partition.Fstype,
			Total:        usage.Total,
			Free:         usage.Free,
			Used:         usage.Used,
			UsedPercent:  usage.UsedPercent,
			TotalGB:      formatBytesForDisk(usage.Total),
			FreeGB:       formatBytesForDisk(usage.Free),
			IsAllowed:    isAllowed,
			IsAccessible: isAccessible,
			IsSSD:        checkIfSSD(partition.Device),
		})
	}
	return disks, nil
}

func getDetailedDiskInfo(diskPath string) (*diskInfoBrief, error) {
	usage, err := disk.Usage(diskPath)
	if err != nil {
		return nil, fmt.Errorf("无法获取磁盘使用情况: %w", err)
	}
	partitions, _ := disk.Partitions(false)
	var partition *disk.PartitionStat
	for i, p := range partitions {
		if normalizeDiskPath(p.Mountpoint) == diskPath {
			partition = &partitions[i]
			break
		}
	}
	if partition == nil {
		return nil, fmt.Errorf("找不到对应的磁盘分区")
	}
	return &diskInfoBrief{
		Path:         diskPath,
		Mountpoint:   partition.Mountpoint,
		Device:       partition.Device,
		Fstype:       partition.Fstype,
		Total:        usage.Total,
		Free:         usage.Free,
		Used:         usage.Used,
		UsedPercent:  usage.UsedPercent,
		TotalGB:      formatBytesForDisk(usage.Total),
		FreeGB:       formatBytesForDisk(usage.Free),
		IsAllowed:    true,
		IsAccessible: true,
		IsSSD:        checkIfSSD(partition.Device),
	}, nil
}

func shouldSkipPartition(partition disk.PartitionStat) bool {
	skipFstypes := map[string]bool{
		"tmpfs": true, "devtmpfs": true, "squashfs": true,
		"overlay": true, "cgroup": true, "cgroup2": true,
	}
	if skipFstypes[partition.Fstype] {
		return true
	}
	skipMountpoints := []string{"/boot", "/snap", "/var/snap"}
	for _, skip := range skipMountpoints {
		if strings.HasPrefix(partition.Mountpoint, skip) {
			return true
		}
	}
	return false
}

func checkIfSSD(device string) bool {
	if runtime.GOOS == "linux" {
		deviceName := strings.TrimPrefix(device, "/dev/")
		data, err := os.ReadFile(fmt.Sprintf("/sys/block/%s/queue/rotational", deviceName))
		if err == nil {
			return strings.TrimSpace(string(data)) == "0"
		}
	}
	return false
}

func normalizeDiskPath(path string) string {
	if runtime.GOOS == "windows" {
		vol := fileVolumeName(path)
		if vol != "" {
			return strings.ToUpper(vol) + "/"
		}
	}
	return filepathClean(path)
}

func fileVolumeName(path string) string {
	if len(path) >= 2 && path[1] == ':' {
		return path[:2]
	}
	return ""
}

func filepathClean(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}

func isPathInList(path string, list []string) bool {
	path = strings.ToUpper(strings.TrimRight(path, "/"))
	for _, item := range list {
		itemNorm := strings.ToUpper(strings.TrimRight(item, "/"))
		if itemNorm == path {
			return true
		}
	}
	return false
}

func isDiskAccessible(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func formatBytesForDisk(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := uint64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
