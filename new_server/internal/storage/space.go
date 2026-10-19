// Package storage 基于本地磁盘实现按账号隔离的上传空间，无需对象存储服务。
package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"syc-file/config"
	"syc-file/internal/model"
)

var ErrDenied = errors.New("无权访问该路径")
var ErrFrozen = errors.New("上传空间已停用或到期，请联系系统管理员")
var ErrUnassigned = errors.New("尚未分配上传空间，请联系系统管理员")
var ErrQuota = errors.New("上传空间剩余容量不足（包含进行中的上传）")

// LockedTransaction 在 MySQL 使用已提交读，避免等待父空间行锁后仍按旧快照统计下级额度。
// SQLite 测试使用默认隔离级别，容量分配同时受到进程内账号锁保护。
func LockedTransaction(db *gorm.DB, fn func(*gorm.DB) error) error {
	opts := &sql.TxOptions{}
	if db.Dialector.Name() == "mysql" {
		opts.Isolation = sql.LevelReadCommitted
	}
	return db.Transaction(fn, opts)
}

type capacityLock struct {
	mu   sync.Mutex
	refs int
}

var capacityLocks = struct {
	sync.Mutex
	entries map[string]*capacityLock
}{entries: make(map[string]*capacityLock)}

// lockCapacity 补充进程内串行控制；跨进程仍由数据库行锁负责。
// 引用归零立即回收，避免长期运行后保留无用用户锁。
func lockCapacity(root string) func() {
	capacityLocks.Lock()
	entry := capacityLocks.entries[root]
	if entry == nil {
		entry = &capacityLock{}
		capacityLocks.entries[root] = entry
	}
	entry.refs++
	capacityLocks.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		capacityLocks.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(capacityLocks.entries, root)
		}
		capacityLocks.Unlock()
	}
}

type View struct {
	model.UploadSpace
	PoolName       string `json:"pool_name"`
	Path           string `json:"path"`
	DefaultPath    string `json:"default_path"`
	UsedBytes      int64  `json:"used_bytes"`
	OwnUsedBytes   int64  `json:"own_used_bytes"`
	AllocatedBytes int64  `json:"allocated_bytes"`
	FileCount      int64  `json:"file_count"`
	Frozen         bool   `json:"frozen"`
}

func User(db *gorm.DB, uid uint) (model.User, error) {
	var u model.User
	err := db.Select("id, level, status, expires_at").First(&u, uid).Error
	if err != nil {
		return u, err
	}
	if u.Status != 1 || (u.ExpiresAt != nil && !u.ExpiresAt.After(time.Now())) {
		return u, ErrDenied
	}
	return u, nil
}

func Root(pool model.StoragePool, space model.UploadSpace) string {
	if space.Directory != "" {
		return filepath.Join(pool.RootPath, space.Directory)
	}
	return filepath.Join(pool.RootPath, "users", strconv.FormatUint(uint64(space.UserID), 10))
}

// Allocated 统计已划给直属下级的容量；停用和到期不会自动收回额度或删除文件。
func Allocated(db *gorm.DB, id uint) (int64, error) {
	var total int64
	err := db.Model(&model.UploadSpace{}).Select("COALESCE(SUM(quota_bytes),0)").Where("parent_id = ?", id).Scan(&total).Error
	return total, err
}

// ActiveTree 沿父空间检查启用状态、有效期和账号状态，防止子空间绕过上级冻结。
func ActiveTree(db *gorm.DB, s model.UploadSpace, p model.StoragePool) error {
	seen := map[uint]bool{}
	for depth := 0; depth < 8; depth++ {
		if seen[s.ID] {
			return ErrFrozen
		}
		seen[s.ID] = true
		if err := Active(s, p); err != nil {
			return err
		}
		if _, err := User(db, s.UserID); err != nil {
			return ErrFrozen
		}
		if s.ParentID == nil {
			return nil
		}
		if err := db.First(&s, *s.ParentID).Error; err != nil {
			return ErrFrozen
		}
	}
	return ErrFrozen
}

// SharedPathActive 按源文件实际所属空间检查分享，系统管理员创建的分享也不能绕过冻结。
func SharedPathActive(db *gorm.DB, path string) error {
	var spaces []model.UploadSpace
	var pools []model.StoragePool
	if db.Find(&spaces).Error != nil || db.Find(&pools).Error != nil {
		return ErrDenied
	}
	poolMap := map[uint]model.StoragePool{}
	for _, p := range pools {
		poolMap[p.ID] = p
	}
	var owner *model.UploadSpace
	var pool model.StoragePool
	length := -1
	for i := range spaces {
		p, ok := poolMap[spaces[i].PoolID]
		if !ok {
			continue
		}
		root := Root(p, spaces[i])
		if len(root) > length && Within(root, path) {
			owner = &spaces[i]
			pool = p
			length = len(root)
		}
	}
	if owner != nil {
		return ActiveTree(db, *owner, pool)
	}
	if config.Conf.IsPathAllowed(path) {
		return nil
	}
	return ErrDenied
}

func Load(db *gorm.DB, uid uint) (model.UploadSpace, model.StoragePool, error) {
	var s model.UploadSpace
	var p model.StoragePool
	if err := db.Where("user_id = ?", uid).First(&s).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return s, p, ErrUnassigned
		}
		return s, p, err
	}
	err := db.First(&p, s.PoolID).Error
	return s, p, err
}
func Active(s model.UploadSpace, p model.StoragePool) error {
	if !s.Enabled || !p.Enabled || (s.ExpiresAt != nil && !s.ExpiresAt.After(time.Now())) {
		return ErrFrozen
	}
	return nil
}

// Within 拒绝路径穿越、同名前缀目录、Windows 备用数据流，以及路径中的符号链接和目录联接。
func Within(root, path string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(path) {
		return false
	}
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	for _, part := range strings.FieldsFunc(rel, func(r rune) bool { return r == '/' || r == '\\' }) {
		if strings.Contains(part, ":") {
			return false
		}
	}
	// 向上检查每一级已存在目录，避免根目录或其祖先被链接替换后越界。
	for current := path; ; current = filepath.Dir(current) {
		fi, err := os.Lstat(current)
		if err != nil && !os.IsNotExist(err) {
			return false
		}
		if err == nil && fi.Mode()&os.ModeSymlink != 0 {
			return false
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	return true
}

// Authorize 依据数据库中的当前账号级别校验路径，不能依赖客户端路径或旧令牌提升权限。
// 只有系统管理员可以访问配置允许的其他目录，普通管理员仍受账号空间限制。
func Authorize(db *gorm.DB, uid uint, path string) error {
	if db == nil {
		if config.Conf.IsPathAllowed(path) {
			return nil
		}
		return ErrDenied
	}
	u, err := User(db, uid)
	if err != nil {
		return ErrDenied
	}
	s, p, err := Load(db, uid)
	if err == nil && Within(Root(p, s), path) {
		rel, _ := filepath.Rel(Root(p, s), path)
		if u.Level < model.LevelSuper && internalPath(rel) {
			return ErrDenied
		}
		if u.Level < model.LevelSuper {
			return ActiveTree(db, s, p)
		}
		return nil
	}
	if u.Level >= model.LevelSuper {
		var pools []model.StoragePool
		if db.Find(&pools).Error != nil {
			return ErrDenied
		}
		for _, pool := range pools {
			if Within(pool.RootPath, path) {
				return nil
			}
		}
	}
	if u.Level >= model.LevelSuper && config.Conf.IsPathAllowed(path) {
		canonical, err := filepath.EvalSymlinks(path)
		if err == nil && !config.Conf.IsPathAllowed(canonical) {
			return ErrDenied
		}
		return nil
	}
	return ErrDenied
}

// Usage 将预分配的 .uploads/*.part 文件也计入容量。
// 预分配文件相当于持久的容量预占，并发上传和进程重启不会丢失这部分用量。
func Usage(root string) (int64, int64, error) {
	var used, count int64
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&os.ModeSymlink != 0 {
			return ErrDenied
		}
		if d.IsDir() {
			if path != root && d.Name() == ".children" {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return ErrDenied
		}
		if info.Size() > int64(^uint64(0)>>1)-used {
			return ErrQuota
		}
		used += info.Size()
		if !strings.Contains(path, string(filepath.Separator)+".uploads"+string(filepath.Separator)) && !strings.Contains(path, string(filepath.Separator)+".versions"+string(filepath.Separator)) {
			count++
		}
		return nil
	})
	return used, count, err
}

func Mine(db *gorm.DB, uid uint) (View, error) {
	s, p, err := Load(db, uid)
	if err != nil {
		return View{}, err
	}
	root := Root(p, s)
	if !Within(root, root) {
		return View{}, ErrDenied
	}
	frozen := ActiveTree(db, s, p) != nil
	if !frozen {
		// 兼容已经分配的空间；根目录丢失时仍报错，不能掩盖磁盘未挂载。
		if err := EnsureDefaultFolder(root); err != nil {
			return View{}, err
		}
	}
	used, count, err := Usage(root)
	if err != nil {
		return View{}, err
	}
	allocated, err := Allocated(db, s.ID)
	if err != nil {
		return View{}, err
	}
	return View{UploadSpace: s, PoolName: p.Name, Path: root, DefaultPath: filepath.Join(root, DefaultFolder), UsedBytes: used + allocated, OwnUsedBytes: used, AllocatedBytes: allocated, FileCount: count, Frozen: frozen}, nil
}

const DefaultFolder = "我的文件"

// EnsureDefaultFolder 只在已有空间内创建默认目录，保留同名目录中的内容。
func EnsureDefaultFolder(root string) error {
	if info, err := os.Stat(root); err != nil {
		return err
	} else if !info.IsDir() {
		return ErrDenied
	}
	path := filepath.Join(root, DefaultFolder)
	if !Within(root, path) {
		return ErrDenied
	}
	return os.MkdirAll(path, 0700)
}

func Temp(db *gorm.DB, uid uint, target, id string) (string, bool) {
	if db == nil {
		return "", false
	}
	s, p, err := Load(db, uid)
	if err != nil || !Within(Root(p, s), target) {
		return "", false
	}
	return filepath.Join(Root(p, s), ".uploads", id+".part"), true
}

// Allocate 使用数据库行锁串行执行容量检查和文件创建，支持多个服务进程。
// 回调必须在返回前创建指定大小的文件；空间内部的临时文件重命名不新增占用。
func Allocate(db *gorm.DB, uid uint, path string, size int64, write func() error) error {
	if size < 0 {
		return ErrQuota
	}
	if db == nil {
		return write()
	}
	u, err := User(db, uid)
	if err != nil {
		return ErrDenied
	}
	s, p, err := Load(db, uid)
	if err != nil || !Within(Root(p, s), path) {
		if u.Level >= model.LevelSuper {
			return write()
		}
		return ErrDenied
	}
	unlock := lockCapacity(Root(p, s))
	defer unlock()
	return LockedTransaction(db, func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&s, s.ID).Error; err != nil {
			return err
		}
		if err := tx.First(&p, s.PoolID).Error; err != nil {
			return err
		}
		if err := ActiveTree(tx, s, p); err != nil {
			return err
		}
		root := Root(p, s)
		if !Within(root, path) {
			return ErrDenied
		}
		used, _, err := Usage(root)
		if err != nil {
			return err
		}
		allocated, err := Allocated(tx, s.ID)
		if err != nil {
			return err
		}
		used += allocated
		old := int64(0)
		if st, err := os.Stat(path); err == nil {
			if !st.Mode().IsRegular() {
				return ErrDenied
			}
			old = st.Size()
		} else if !os.IsNotExist(err) {
			return err
		}
		if s.QuotaBytes <= 0 || size > s.QuotaBytes || used-old > s.QuotaBytes-size {
			return ErrQuota
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return fmt.Errorf("创建上传目录失败: %w", err)
		}
		return write()
	})
}
