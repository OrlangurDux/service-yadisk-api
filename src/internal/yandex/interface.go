package yandex

import (
	"context"

	"service-yadisk-api/internal/models"
)

// ClientInterface определяет интерфейс для клиента Яндекс.Диска
type ClientInterface interface {
	GetDiskInfo(ctx context.Context) (*models.DiskInfo, error)
	CreateFolder(ctx context.Context, remotePath string) error
	GetResource(ctx context.Context, remotePath string) (*models.Resource, error)
	ListFolderResources(ctx context.Context, remotePath string, limit, offset int, fields string) (*models.ResourceList, error)
	GetAllResources(ctx context.Context, remotePath string) ([]models.Resource, error)
	GetResourcesByPath(ctx context.Context, remotePath string) ([]models.Resource, error)
	UploadFile(ctx context.Context, localPath, remotePath string) error
	DownloadFile(ctx context.Context, remotePath, localPath string) error
	DeleteResource(ctx context.Context, remotePath string, permanently bool) error
	EnsureFolderExists(ctx context.Context, remotePath string) error
	GetResourceMetadata(ctx context.Context, remotePath string, fields []string) (*models.Resource, error)
	CheckOperationStatus(ctx context.Context, operationURL string) (string, error)
}

// Убедимся, что Client реализует ClientInterface
var _ ClientInterface = (*Client)(nil)
