package sync

type FileChangeReport struct {
	FolderID     uint64 `json:"folder_id"`
	RelativePath string `json:"relative_path"`
	FileName     string `json:"file_name"`
	Action       string `json:"action"`
	FileSize     int64  `json:"file_size"`
	FileHash     string `json:"file_hash"`
	IsDir        bool   `json:"is_dir"`
	Mtime        int64  `json:"mtime"`
}

type ScanItem struct {
	RelativePath string `json:"relative_path"`
	FileName     string `json:"file_name"`
	FileSize     int64  `json:"file_size"`
	FileHash     string `json:"file_hash"`
	IsDir        bool   `json:"is_dir"`
	Mtime        int64  `json:"mtime"`
}

type ScanReport struct {
	FolderID uint64     `json:"folder_id"`
	Items    []ScanItem `json:"items"`
}

type TaskNotify struct {
	Event        string `json:"event"`
	TaskID       uint64 `json:"task_id"`
	TaskType     string `json:"task_type"`
	Direction    string `json:"direction"`
	FolderID     uint64 `json:"folder_id"`
	RelativePath string `json:"relative_path"`
	FileName     string `json:"file_name"`
	FileSize     int64  `json:"file_size"`
	FileHash     string `json:"file_hash"`
	RemotePath   string `json:"remote_path"`
	RemoteDir    string `json:"remote_dir"`
}
