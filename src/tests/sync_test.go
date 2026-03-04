package tests

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"service-yadisk-api/internal/config"
	"service-yadisk-api/internal/sync"
	"service-yadisk-api/internal/yandex"
)

// MockClient реализует yandex.ClientInterface для тестов
type MockClient struct {
	Folders   map[string]bool
	Files     map[string]*yandex.Resource
	Uploads   []string
	Downloads []string
	Deletes   []string
	Errors    []error
}

func NewMockClient() *MockClient {
	return &MockClient{
		Folders:   make(map[string]bool),
		Files:     make(map[string]*yandex.Resource),
		Uploads:   []string{},
		Downloads: []string{},
		Deletes:   []string{},
		Errors:    []error{},
	}
}

func (m *MockClient) GetDiskInfo() (*yandex.DiskInfo, error) {
	if len(m.Errors) > 0 {
		err := m.Errors[0]
		m.Errors = m.Errors[1:]
		return nil, err
	}

	return &yandex.DiskInfo{
		TotalSpace: 1000000000,
		UsedSpace:  500000000,
		TrashSize:  1000000,
		SystemFolders: struct {
			Applications string `json:"applications"`
			Downloads    string `json:"downloads"`
		}{
			Applications: "disk:/Приложения",
			Downloads:    "disk:/Загрузки",
		},
	}, nil
}

func (m *MockClient) CreateFolder(remotePath string) error {
	if len(m.Errors) > 0 {
		err := m.Errors[0]
		m.Errors = m.Errors[1:]
		return err
	}

	m.Folders[remotePath] = true
	return nil
}

func (m *MockClient) GetResource(remotePath string) (*yandex.Resource, error) {
	if len(m.Errors) > 0 {
		err := m.Errors[0]
		m.Errors = m.Errors[1:]
		return nil, err
	}

	if resource, exists := m.Files[remotePath]; exists {
		return resource, nil
	}
	if m.Folders[remotePath] {
		return &yandex.Resource{
			Path: remotePath,
			Type: "dir",
			Name: filepath.Base(remotePath),
		}, nil
	}
	return nil, nil
}

func (m *MockClient) ListFolderResources(remotePath string, limit, offset int, fields string) (*yandex.ResourceList, error) {
	if len(m.Errors) > 0 {
		err := m.Errors[0]
		m.Errors = m.Errors[1:]
		return nil, err
	}

	var items []yandex.Resource
	for path, resource := range m.Files {
		// Проверяем, находится ли файл в указанной папке
		if len(path) >= len(remotePath) && path[:len(remotePath)] == remotePath &&
			(len(path) == len(remotePath) || path[len(remotePath)] == '/') {
			items = append(items, *resource)
		}
	}

	return &yandex.ResourceList{
		Items:  items,
		Limit:  limit,
		Offset: offset,
		Path:   remotePath,
		Total:  len(items),
	}, nil
}

func (m *MockClient) GetAllResources(remotePath string) ([]yandex.Resource, error) {
	if len(m.Errors) > 0 {
		err := m.Errors[0]
		m.Errors = m.Errors[1:]
		return nil, err
	}

	var resources []yandex.Resource
	for _, resource := range m.Files {
		resources = append(resources, *resource)
	}
	return resources, nil
}

func (m *MockClient) GetResourcesByPath(remotePath string) ([]yandex.Resource, error) {
	return m.GetAllResources(remotePath)
}

func (m *MockClient) UploadFile(localPath, remotePath string) error {
	if len(m.Errors) > 0 {
		err := m.Errors[0]
		m.Errors = m.Errors[1:]
		return err
	}

	m.Uploads = append(m.Uploads, remotePath)

	// Читаем файл для вычисления размера
	info, err := os.Stat(localPath)
	if err != nil {
		return err
	}

	// Создаем мок-ресурс
	m.Files[remotePath] = &yandex.Resource{
		Path:     remotePath,
		Type:     "file",
		Name:     filepath.Base(remotePath),
		Size:     info.Size(),
		MD5:      "mock-md5-hash-" + remotePath, // Уникальный для каждого файла
		Created:  time.Now().Format(time.RFC3339),
		Modified: time.Now().Format(time.RFC3339),
		MimeType: "application/octet-stream",
	}

	// Создаем родительскую папку
	parentDir := filepath.Dir(remotePath)
	if parentDir != "." && parentDir != "/" {
		m.Folders[parentDir] = true
	}

	return nil
}

func (m *MockClient) DownloadFile(remotePath, localPath string) error {
	if len(m.Errors) > 0 {
		err := m.Errors[0]
		m.Errors = m.Errors[1:]
		return err
	}

	m.Downloads = append(m.Downloads, remotePath)

	// Создаем директорию, если не существует
	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// В моке создаем файл с тестовым содержимым
	return os.WriteFile(localPath, []byte("mock content from "+remotePath), 0644)
}

func (m *MockClient) DeleteResource(remotePath string, permanently bool) error {
	if len(m.Errors) > 0 {
		err := m.Errors[0]
		m.Errors = m.Errors[1:]
		return err
	}

	m.Deletes = append(m.Deletes, remotePath)
	delete(m.Files, remotePath)
	return nil
}

func (m *MockClient) EnsureFolderExists(remotePath string) error {
	if len(m.Errors) > 0 {
		err := m.Errors[0]
		m.Errors = m.Errors[1:]
		return err
	}

	// Разбиваем путь на части и создаем все родительские папки
	parts := []string{}
	current := ""

	for _, part := range filepath.SplitList(remotePath) {
		if part == "" || part == "." {
			continue
		}
		if current == "" {
			current = part
		} else {
			current = current + "/" + part
		}
		parts = append(parts, current)
	}

	for _, folder := range parts {
		m.Folders[folder] = true
	}

	return nil
}

func (m *MockClient) GetResourceMetadata(remotePath string, fields []string) (*yandex.Resource, error) {
	return m.GetResource(remotePath)
}

func (m *MockClient) CheckOperationStatus(operationURL string) (string, error) {
	return "success", nil
}

func TestCalculateFileMD5(t *testing.T) {
	// Создаем временный файл с известным содержимым
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")

	content := []byte("Hello, World! This is a test file for MD5 calculation.")

	err := os.WriteFile(testFile, content, 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Вычисляем MD5
	md5, err := sync.CalculateFileMD5(testFile)
	if err != nil {
		t.Fatalf("Failed to calculate MD5: %v", err)
	}

	// MD5 строки "Hello, World! This is a test file for MD5 calculation."
	expectedMD5 := "b10a8db164e0754105b7a99be72e3fe5"

	if md5 != expectedMD5 {
		t.Errorf("Expected MD5 '%s', got '%s'", expectedMD5, md5)
	}
}

func TestSyncer_NeedsSyncBasedOnMD5(t *testing.T) {
	// Создаем временные файлы
	tmpDir := t.TempDir()

	file1 := filepath.Join(tmpDir, "file1.txt")
	file2 := filepath.Join(tmpDir, "file2.txt")

	// Файлы с одинаковым содержимым
	sameContent := []byte("Same content for MD5 test")
	os.WriteFile(file1, sameContent, 0644)
	os.WriteFile(file2, sameContent, 0644)

	// Получаем информацию о файлах
	info1, _ := os.Stat(file1)
	info2, _ := os.Stat(file2)

	md5_1, _ := sync.CalculateFileMD5(file1)
	md5_2, _ := sync.CalculateFileMD5(file2)

	fileInfo1 := sync.FileInfo{
		Path:     file1,
		Size:     info1.Size(),
		Modified: info1.ModTime(),
		MD5:      md5_1,
		IsDir:    false,
	}

	fileInfo2 := sync.FileInfo{
		Path:     file2,
		Size:     info2.Size(),
		Modified: info2.ModTime(),
		MD5:      md5_2,
		IsDir:    false,
	}

	// Создаем синхронизатор для теста
	cfg := &config.Config{
		Token:         "test-token",
		SyncFrequency: time.Minute * 5,
	}

	mockClient := NewMockClient()
	syncer := sync.NewSyncer(mockClient, cfg)

	// Тест 1: Файлы с одинаковым MD5 не нуждаются в синхронизации
	if syncer.NeedsSyncBasedOnMD5(fileInfo1, fileInfo2) {
		t.Error("Files with same MD5 should not need sync")
	}

	// Тест 2: Файлы с разным размером нуждаются в синхронизации
	fileInfo3 := sync.FileInfo{
		Path:     file1,
		Size:     info1.Size() + 100, // Другой размер
		Modified: info1.ModTime(),
		MD5:      md5_1,
		IsDir:    false,
	}

	if !syncer.NeedsSyncBasedOnMD5(fileInfo1, fileInfo3) {
		t.Error("Files with different size should need sync")
	}

	// Тест 3: Файлы с разным MD5 нуждаются в синхронизации
	fileInfo4 := sync.FileInfo{
		Path:     file1,
		Size:     info1.Size(),
		Modified: info1.ModTime(),
		MD5:      "different-md5-hash",
		IsDir:    false,
	}

	if !syncer.NeedsSyncBasedOnMD5(fileInfo1, fileInfo4) {
		t.Error("Files with different MD5 should need sync")
	}
}

func TestBidirectionalSync_NewFiles(t *testing.T) {
	// Создаем тестовые директории
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "local")
	remoteDir := "disk:/remote"

	os.MkdirAll(localDir, 0755)

	// Создаем локальные файлы
	file1 := filepath.Join(localDir, "file1.txt")
	file2 := filepath.Join(localDir, "file2.txt")

	os.WriteFile(file1, []byte("File 1 content for sync test"), 0644)
	os.WriteFile(file2, []byte("File 2 content for sync test"), 0644)

	// Создаем поддиректорию
	subDir := filepath.Join(localDir, "subdir")
	os.MkdirAll(subDir, 0755)

	file3 := filepath.Join(subDir, "file3.txt")
	os.WriteFile(file3, []byte("File 3 content in subdirectory"), 0644)

	// Создаем конфигурацию и мок-клиент
	cfg := &config.Config{
		Token:         "test-token",
		SyncFrequency: time.Minute * 5,
		Folders: []config.FolderMapping{
			{
				Local:      localDir,
				Remote:     remoteDir,
				SyncMode:   "bidirectional",
				DeleteMode: "none",
			},
		},
	}

	mockClient := NewMockClient()
	syncer := sync.NewSyncer(mockClient, cfg)

	// Выполняем синхронизацию
	result, err := syncer.BidirectionalSync(localDir, remoteDir, "bidirectional", "none")
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	// Проверяем, что файлы были загружены
	if len(result.Uploaded) != 3 {
		t.Errorf("Expected 3 files uploaded, got %d. Uploaded: %v", len(result.Uploaded), result.Uploaded)
	}

	// Проверяем, что файлы были созданы в мок-клиенте
	expectedFiles := []string{
		"disk:/remote/file1.txt",
		"disk:/remote/file2.txt",
		"disk:/remote/subdir/file3.txt",
	}

	for _, expectedFile := range expectedFiles {
		if _, exists := mockClient.Files[expectedFile]; !exists {
			t.Errorf("Expected file not found in mock client: %s", expectedFile)
		}
	}

	// Проверяем, что папки были созданы
	expectedFolders := []string{
		"disk:/remote",
		"disk:/remote/subdir",
	}

	for _, expectedFolder := range expectedFolders {
		if !mockClient.Folders[expectedFolder] {
			t.Errorf("Expected folder not found in mock client: %s", expectedFolder)
		}
	}

	// Проверяем статистику
	stats := syncer.GetStatistics()
	if stats.TotalUploaded != 3 {
		t.Errorf("Expected total uploaded 3, got %d", stats.TotalUploaded)
	}
}

func TestBidirectionalSync_ExistingFiles(t *testing.T) {
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "local")
	remoteDir := "disk:/remote"

	os.MkdirAll(localDir, 0755)

	// Создаем файл локально
	localFile := filepath.Join(localDir, "test.txt")
	os.WriteFile(localFile, []byte("Local version with different content"), 0644)

	// Создаем мок-клиент с существующим файлом
	mockClient := NewMockClient()

	// Добавляем удаленный файл с тем же именем, но другим содержимым
	mockClient.Files[remoteDir+"/test.txt"] = &yandex.Resource{
		Path:     remoteDir + "/test.txt",
		Type:     "file",
		Name:     "test.txt",
		Size:     100,
		MD5:      "remote-md5-hash-different",
		Created:  time.Now().Format(time.RFC3339),
		Modified: time.Now().Add(-time.Hour).Format(time.RFC3339),
		MimeType: "text/plain",
	}

	cfg := &config.Config{
		Token:         "test-token",
		SyncFrequency: time.Minute * 5,
	}

	syncer := sync.NewSyncer(mockClient, cfg)

	// Синхронизация должна обнаружить разницу в MD5 и загрузить новую версию
	result, err := syncer.BidirectionalSync(localDir, remoteDir, "bidirectional", "none")
	if err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	// Должен быть загружен 1 файл (так как MD5 различается)
	if len(result.Uploaded) != 1 {
		t.Errorf("Expected 1 file uploaded (due to MD5 mismatch), got %d. Uploaded: %v", len(result.Uploaded), result.Uploaded)
	}

	// Проверяем, что файл был загружен
	if len(mockClient.Uploads) != 1 {
		t.Errorf("Expected 1 upload, got %d", len(mockClient.Uploads))
	}

	// Проверяем, что правильный файл был загружен
	expectedUpload := "disk:/remote/test.txt"
	if len(mockClient.Uploads) > 0 && mockClient.Uploads[0] != expectedUpload {
		t.Errorf("Expected upload of %s, got %s", expectedUpload, mockClient.Uploads[0])
	}
}

func TestBidirectionalSync_DownloadOnly(t *testing.T) {
	tmpDir := t.TempDir()
	localDir := filepath.Join(tmpDir, "local")
	remoteDir := "disk:/remote"

	os.MkdirAll(localDir, 0755)

	// Создаем мок-клиент с существующим удаленным файлом
	mockClient := NewMockClient()

	// Добавляем удаленный файл
	mockClient.Files[remoteDir+"/remote-only.txt"] = &yandex.Resource{
		Path:     remoteDir + "/remote-only.txt",
		Type:     "file",
		Name:     "remote-only.txt",
		Size:     500,
		MD5:      "remote-only-md5",
		Created:  time.Now().Format(time.RFC3339),
		Modified: time.Now().Add(-30 * time.Minute).Format(time.RFC3339),
		MimeType: "text/plain",
	}

	cfg := &config.Config{
		Token:         "test-token",
		SyncFrequency: time.Minute * 5,
	}

	syncer := sync.NewSyncer(mockClient, cfg)

	// Синхронизация только в режиме download
	result, err := syncer.BidirectionalSync(localDir, remoteDir, "download", "none")
	if err != nil {
		t.Fatalf("Download sync failed: %v", err)
	}

	// Должен быть скачан 1 файл
	if len(result.Downloaded) != 1 {
		t.Errorf("Expected 1 file downloaded, got %d. Downloaded: %v", len(result.Downloaded), result.Downloaded)
	}

	// Проверяем, что файл был скачан
	if len(mockClient.Downloads) != 1 {
		t.Errorf("Expected 1 download, got %d", len(mockClient.Downloads))
	}

	// Проверяем, что upload не выполнялся
	if len(result.Uploaded) != 0 {
		t.Errorf("Expected 0 uploads in download mode, got %d", len(result.Uploaded))
	}
}
