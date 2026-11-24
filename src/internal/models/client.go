package models

type DiskInfo struct {
	TotalSpace    int64 `json:"total_space"`
	UsedSpace     int64 `json:"used_space"`
	TrashSize     int64 `json:"trash_size"`
	SystemFolders struct {
		Applications string `json:"applications"`
		Downloads    string `json:"downloads"`
	} `json:"system_folders"`
}

type Resource struct {
	Path             string            `json:"path"`
	Type             string            `json:"type"`
	Name             string            `json:"name"`
	Size             int64             `json:"size"`
	MD5              string            `json:"md5"`
	Created          string            `json:"created"`
	Modified         string            `json:"modified"`
	MimeType         string            `json:"mime_type"`
	Embedded         *ResourceList     `json:"_embedded,omitempty"`
	CustomProperties map[string]string `json:"custom_properties,omitempty"`
}

type ResourceList struct {
	Items  []Resource `json:"items"`
	Limit  int        `json:"limit"`
	Offset int        `json:"offset"`
	Path   string     `json:"path"`
	Total  int        `json:"total"`
	Sort   string     `json:"sort"`
}

type UploadLink struct {
	Href      string `json:"href"`
	Method    string `json:"method"`
	Templated bool   `json:"templated"`
}

type DownloadLink struct {
	Href      string `json:"href"`
	Method    string `json:"method"`
	Templated bool   `json:"templated"`
}

type Operation struct {
	Status string `json:"status"`
}
