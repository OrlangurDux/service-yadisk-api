package yandex

// ClientInterface определяет интерфейс для клиента Яндекс.Диска
type ClientInterface interface {
	GetDiskInfo() (*DiskInfo, error)
	CreateFolder(remotePath string) error
	GetResource(remotePath string) (*Resource, error)
	ListFolderResources(remotePath string, limit, offset int, fields string) (*ResourceList, error)
	GetAllResources(remotePath string) ([]Resource, error)
	GetResourcesByPath(remotePath string) ([]Resource, error)
	UploadFile(localPath, remotePath string) error
	DownloadFile(remotePath, localPath string) error
	DeleteResource(remotePath string, permanently bool) error
	EnsureFolderExists(remotePath string) error
	GetResourceMetadata(remotePath string, fields []string) (*Resource, error)
	CheckOperationStatus(operationURL string) (string, error)
}

// Убедимся, что Client реализует ClientInterface
var _ ClientInterface = (*Client)(nil)
