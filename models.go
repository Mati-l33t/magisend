package main

type File struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Offset   int64  `json:"offset"`
	Backend  string `json:"backend"`
	HasThumb bool   `json:"has_thumb"`
	// The file's folder below a shared folder, such as "Photos/2024".
	Folder string `json:"folder,omitempty"`
	// Download statistics; Remaining is set only when the share has a limit.
	Downloads    int64  `json:"downloads,omitempty"`
	LastDownload int64  `json:"last_download,omitempty"`
	Remaining    *int64 `json:"remaining,omitempty"`
}
type Share struct {
	Mode          string `json:"mode,omitempty"`
	FolderID      string `json:"folder_id,omitempty"`
	ID            string `json:"id"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	Slug          string `json:"slug"`
	Expires       int64  `json:"expires"`
	Created       int64  `json:"created"`
	Published     bool   `json:"published"`
	Protected     bool   `json:"protected"`
	Files         []File `json:"files"`
	Downloads     int64  `json:"downloads"`
	Notify        bool   `json:"notify"`
	DownloadLimit int64  `json:"download_limit"`
	LastDownload  int64  `json:"last_download"`
}
