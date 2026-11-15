package config

import (
	"reflect"
	"sort"
	"strings"
)

// 热字段清单（HANDOFF §5）：保存后不需要重启后端，下一次读取立即生效。
//
// 原则：拿不准的一律当冷字段。误标热字段会把并发写引入请求路径（数据竞争），
// 代价远大于多一次重启；冷字段只写文件、不动 Live，等用户挑时间重启即可。
var hotPaths = []string{
	"log.level",

	"monitor.sys_detail_interval_seconds",
	"monitor.process_top_n",

	"file.allowed_paths",
	"file.upload.max_file_size",
	"file.upload.max_filename_length",
	"file.upload.allowed_extensions",
	"file.upload.forbidden_extensions",

	"whitelist",

	"auth.token_expire",
	"auth.refresh_expire",

	"share.max_expire_minutes",

	"quick_share.default_expire_minutes",
	"quick_share.max_capacity_bytes",
	"quick_share.memory_threshold_bytes",

	"user.max_size",
}

// hotPathSet 供 IsHot 做 O(1) 精确匹配。
var hotPathSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(hotPaths))
	for _, p := range hotPaths {
		m[p] = struct{}{}
	}
	return m
}()

// HotPaths 返回全部热字段的点分路径（返回副本，调用方可以随意修改）。
func HotPaths() []string {
	return append([]string(nil), hotPaths...)
}

// IsHot 判断点分路径是否属于热字段（精确匹配，区分大小写）。
func IsHot(path string) bool {
	_, ok := hotPathSet[path]
	return ok
}

// ApplyHotInto 把 next 里的热字段搬到 dst，返回本次真正发生变化的热字段路径。
//
// 只搬热字段：冷字段必须保持 dst 里的旧值（进程还在按旧值运行，改 Live 没有意义，
// 反而会让 GUI 读到的“当前配置”和进程实际行为不一致）。
// dst 通常应该是 Clone(Live()) 出来的副本，改完再 PublishLive；不要直接传 Live()，
// 那会把正在被读的快照改掉。
func ApplyHotInto(dst, next *Config) []string {
	if dst == nil || next == nil {
		return nil
	}
	reloaded := make([]string, 0, len(hotPaths))

	if dst.Log.Level != next.Log.Level {
		dst.Log.Level = next.Log.Level
		reloaded = append(reloaded, "log.level")
	}

	if dst.Monitor.SysDetailIntervalSeconds != next.Monitor.SysDetailIntervalSeconds {
		dst.Monitor.SysDetailIntervalSeconds = next.Monitor.SysDetailIntervalSeconds
		reloaded = append(reloaded, "monitor.sys_detail_interval_seconds")
	}
	if dst.Monitor.ProcessTopN != next.Monitor.ProcessTopN {
		dst.Monitor.ProcessTopN = next.Monitor.ProcessTopN
		reloaded = append(reloaded, "monitor.process_top_n")
	}

	if !reflect.DeepEqual(dst.File.AllowedPaths, next.File.AllowedPaths) {
		dst.File.AllowedPaths = cloneStrings(next.File.AllowedPaths)
		reloaded = append(reloaded, "file.allowed_paths")
	}
	if dst.File.Upload.MaxFileSize != next.File.Upload.MaxFileSize {
		dst.File.Upload.MaxFileSize = next.File.Upload.MaxFileSize
		reloaded = append(reloaded, "file.upload.max_file_size")
	}
	if dst.File.Upload.MaxFilenameLength != next.File.Upload.MaxFilenameLength {
		dst.File.Upload.MaxFilenameLength = next.File.Upload.MaxFilenameLength
		reloaded = append(reloaded, "file.upload.max_filename_length")
	}
	if !reflect.DeepEqual(dst.File.Upload.AllowedExtensions, next.File.Upload.AllowedExtensions) {
		dst.File.Upload.AllowedExtensions = cloneStrings(next.File.Upload.AllowedExtensions)
		reloaded = append(reloaded, "file.upload.allowed_extensions")
	}
	if !reflect.DeepEqual(dst.File.Upload.ForbiddenExtensions, next.File.Upload.ForbiddenExtensions) {
		dst.File.Upload.ForbiddenExtensions = cloneStrings(next.File.Upload.ForbiddenExtensions)
		reloaded = append(reloaded, "file.upload.forbidden_extensions")
	}

	if !reflect.DeepEqual(dst.Whitelist, next.Whitelist) {
		dst.Whitelist = cloneStrings(next.Whitelist)
		reloaded = append(reloaded, "whitelist")
	}

	if dst.Auth.TokenExpire != next.Auth.TokenExpire {
		dst.Auth.TokenExpire = next.Auth.TokenExpire
		reloaded = append(reloaded, "auth.token_expire")
	}
	if dst.Auth.RefreshExpire != next.Auth.RefreshExpire {
		dst.Auth.RefreshExpire = next.Auth.RefreshExpire
		reloaded = append(reloaded, "auth.refresh_expire")
	}

	if dst.Share.MaxExpireMinutes != next.Share.MaxExpireMinutes {
		dst.Share.MaxExpireMinutes = next.Share.MaxExpireMinutes
		reloaded = append(reloaded, "share.max_expire_minutes")
	}

	if dst.QuickShare.DefaultExpireMinutes != next.QuickShare.DefaultExpireMinutes {
		dst.QuickShare.DefaultExpireMinutes = next.QuickShare.DefaultExpireMinutes
		reloaded = append(reloaded, "quick_share.default_expire_minutes")
	}
	if dst.QuickShare.MaxCapacityBytes != next.QuickShare.MaxCapacityBytes {
		dst.QuickShare.MaxCapacityBytes = next.QuickShare.MaxCapacityBytes
		reloaded = append(reloaded, "quick_share.max_capacity_bytes")
	}
	if dst.QuickShare.MemoryThresholdBytes != next.QuickShare.MemoryThresholdBytes {
		dst.QuickShare.MemoryThresholdBytes = next.QuickShare.MemoryThresholdBytes
		reloaded = append(reloaded, "quick_share.memory_threshold_bytes")
	}

	if dst.User.MaxSize != next.User.MaxSize {
		dst.User.MaxSize = next.User.MaxSize
		reloaded = append(reloaded, "user.max_size")
	}

	return reloaded
}

// ClassifyChanges 对比两份配置，按热字段表把变化的字段分成
// “可即时生效（reloaded）”和“需重启（needRestart）”两组。
//
// GUI 保存后用它给用户提示；后端 watcher 只关心 reloaded，needRestart 记日志即可。
// 字段路径由 Config 的 mapstructure tag 推导，新增字段自动纳入比较，不需要维护第二份清单。
// oldC 传 nil（例如首次创建配置文件）时按“全零配置”比较。
func ClassifyChanges(oldC, newC *Config) (reloaded, needRestart []string) {
	if newC == nil {
		return nil, nil
	}
	oldLeaves := configLeafValues(oldC)
	newLeaves := configLeafValues(newC)

	for path, nv := range newLeaves {
		ov := oldLeaves[path]
		if oldC != nil && reflect.DeepEqual(ov, nv) {
			continue
		}
		if IsHot(path) {
			reloaded = append(reloaded, path)
		} else {
			needRestart = append(needRestart, path)
		}
	}
	sort.Strings(reloaded)
	sort.Strings(needRestart)
	return reloaded, needRestart
}

// configLeafValues 把配置拍平成 “点分路径 → 叶子值”。
// 结构体递归展开；slice / map / 标量都算叶子（slice 整体比较，避免元素下标成为路径）。
func configLeafValues(c *Config) map[string]any {
	out := make(map[string]any)
	if c == nil {
		return out
	}
	collectLeafValues(reflect.ValueOf(c).Elem(), "", out)
	return out
}

func collectLeafValues(v reflect.Value, prefix string, out map[string]any) {
	if v.Kind() == reflect.Struct {
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := f.Name
			if tag, ok := f.Tag.Lookup("mapstructure"); ok {
				tag = strings.Split(tag, ",")[0]
				if tag == "-" {
					continue
				}
				if tag != "" {
					name = tag
				}
			}
			path := name
			if prefix != "" {
				path = prefix + "." + name
			}
			collectLeafValues(v.Field(i), path, out)
		}
		return
	}
	out[prefix] = v.Interface()
}
