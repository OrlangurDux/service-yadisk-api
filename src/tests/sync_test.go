package tests

import (
	"context"
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"service-yadisk-api/internal/config"
	"service-yadisk-api/internal/logger"
	"service-yadisk-api/internal/models"
	syncer "service-yadisk-api/internal/sync"
)

// MockClient реализует yandex.ClientInterface для тестов
type MockClient struct {
	mu        sync.Mutex
	Folders   map[string]bool
	Files     map[string]*models.Resource
	Contents  map[string][]byte
	Uploads   []string
	Downloads []string
	Deletes   []string
	Err       error // если задан — все вызовы возвращают эту ошибку
}

func NewMockClient() *MockClient {
	return &MockClient{
		Folders:   map[string]bool{"disk:/": true},
		Files:     make(map[string]*models.Resource),
		Contents:  make(map[string][]byte),
		Uploads:   []string{},
		Downloads: []string{},
		Deletes:   []string{},
	}
}

func (m *MockClient) fail() error { return m.Err }

func (m *MockClient) GetDiskInfo(ctx context.Context) (*models.DiskInfo, error) {
	if err := m.fail(); err != nil {
		return nil, err
	}
	return &models.DiskInfo{TotalSpace: 1 << 30, UsedSpace: 1 << 29, TrashSize: 1 << 20}, nil
}

func (m *MockClient) CreateFolder(ctx context.Context, remotePath string) error {
	if err := m.fail(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Folders[remotePath] = true
	return nil
}

func (m *MockClient) GetResource(ctx context.Context, remotePath string) (*models.Resource, error) {
	if err := m.fail(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.Files[remotePath]; ok {
		return r, nil
	}
	if m.Folders[remotePath] {
		return &models.Resource{Path: remotePath, Type: "dir", Name: filepath.Base(remotePath)}, nil
	}
	return nil, nil
}

func (m *MockClient) ListFolderResources(ctx context.Context, remotePath string, limit, offset int, fields string) (*models.ResourceList, error) {
	return nil, fmt.Errorf("not implemented in mock")
}

func (m *MockClient) GetAllResources(ctx context.Context, remotePath string) ([]models.Resource, error) {
	if err := m.fail(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []models.Resource
	for p, r := range m.Files {
		if p == remotePath || strings.HasPrefix(p, remotePath+"/") {
			out = append(out, *r)
		}
	}
	for p := range m.Folders {
		if p != remotePath && strings.HasPrefix(p, remotePath+"/") {
			out = append(out, models.Resource{Path: p, Type: "dir", Name: filepath.Base(p)})
		}
	}
	return out, nil
}

func (m *MockClient) GetResourcesByPath(ctx context.Context, remotePath string) ([]models.Resource, error) {
	return m.GetAllResources(ctx, remotePath)
}

func (m *MockClient) UploadFile(ctx context.Context, localPath, remotePath string) error {
	if err := m.fail(); err != nil {
		return err
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Uploads = append(m.Uploads, remotePath)
	m.Contents[remotePath] = data
	m.Files[remotePath] = &models.Resource{
		Path:     remotePath,
		Type:     "file",
		Name:     filepath.Base(remotePath),
		Size:     int64(len(data)),
		MD5:      fmt.Sprintf("%x", md5.Sum(data)),
		Modified: time.Now().Format(time.RFC3339),
	}
	// Фиксируем родительскую папку как существующую
	m.Folders[filepath.ToSlash(filepath.Dir(remotePath))] = true
	return nil
}

func (m *MockClient) DownloadFile(ctx context.Context, remotePath, localPath string) error {
	if err := m.fail(); err != nil {
		return err
	}
	m.mu.Lock()
	content, ok := m.Contents[remotePath]
	m.Downloads = append(m.Downloads, remotePath)
	m.mu.Unlock()
	if !ok {
		content = []byte("mock content from " + remotePath)
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return err
	}
	return os.WriteFile(localPath, content, 0644)
}

func (m *MockClient) DeleteResource(ctx context.Context, remotePath string, permanently bool) error {
	if err := m.fail(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Deletes = append(m.Deletes, remotePath)
	delete(m.Files, remotePath)
	delete(m.Contents, remotePath)
	return nil
}

func (m *MockClient) EnsureFolderExists(ctx context.Context, remotePath string) error {
	if err := m.fail(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	clean := strings.TrimPrefix(remotePath, "disk:/")
	current := ""
	for _, part := range strings.Split(clean, "/") {
		if part == "" {
			continue
		}
		if current == "" {
			current = "disk:/" + part
		} else {
			current = current + "/" + part
		}
		m.Folders[current] = true
	}
	return nil
}

func (m *MockClient) GetResourceMetadata(ctx context.Context, remotePath string, fields []string) (*models.Resource, error) {
	return m.GetResource(ctx, remotePath)
}

func (m *MockClient) CheckOperationStatus(ctx context.Context, operationURL string) (string, error) {
	return "success", nil
}

// ---- helpers ---------------------------------------------------------------

func testConfig(t *testing.T, folders ...config.FolderMapping) *config.Config {
	t.Helper()
	return &config.Config{
		Token:         "test-token",
		SyncFrequency: time.Minute,
		Folders:       folders,
		CacheEnabled:  true,
		CachePath:     filepath.Join(t.TempDir(), "cache.json"),
	}
}

func newTestSyncer(t *testing.T, cfg *config.Config, client *MockClient) *syncer.Syncer {
	t.Helper()
	s, err := syncer.NewSyncer(client, cfg, logger.NilLogger{})
	if err != nil {
		t.Fatalf("NewSyncer failed: %v", err)
	}
	return s
}

func writeLocal(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func addRemoteFile(m *MockClient, path, content string, modified time.Time) {
	m.Files[path] = &models.Resource{
		Path:     path,
		Type:     "file",
		Name:     filepath.Base(path),
		Size:     int64(len(content)),
		MD5:      fmt.Sprintf("%x", md5.Sum([]byte(content))),
		Modified: modified.Format(time.RFC3339),
	}
	m.Contents[path] = []byte(content)
}

// ---- Тесты -----------------------------------------------------------------

func TestCalculateFileMD5(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("Hello, World! This is a test file for MD5 calculation.")
	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatal(err)
	}

	got, err := syncer.CalculateFileMD5(testFile)
	if err != nil {
		t.Fatal(err)
	}
	if want := "2025cbae4298c7a5c562808f864510ea"; got != want {
		t.Errorf("Expected MD5 %q, got %q", want, got)
	}
}

func TestSync_UploadNewFiles(t *testing.T) {
	localDir := filepath.Join(t.TempDir(), "local")
	remoteDir := "disk:/remote"

	writeLocal(t, localDir, "file1.txt", "File 1 content")
	writeLocal(t, localDir, "file2.txt", "File 2 content")
	writeLocal(t, localDir, "subdir/file3.txt", "File 3 in subdir")

	mock := NewMockClient()
	cfg := testConfig(t)
	s := newTestSyncer(t, cfg, mock)

	result := s.BidirectionalSync(context.Background(), localDir, remoteDir, "bidirectional", "none")

	if len(result.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", result.Errors)
	}
	if len(result.Uploaded) != 3 {
		t.Errorf("Expected 3 uploads, got %d: %v", len(result.Uploaded), result.Uploaded)
	}
	for _, p := range []string{"disk:/remote/file1.txt", "disk:/remote/file2.txt", "disk:/remote/subdir/file3.txt"} {
		if _, ok := mock.Files[p]; !ok {
			t.Errorf("Expected remote file %s", p)
		}
	}
	if !mock.Folders["disk:/remote/subdir"] {
		t.Error("Expected remote subdir to be created")
	}
}

func TestSync_SkipIdenticalFiles(t *testing.T) {
	localDir := filepath.Join(t.TempDir(), "local")
	remoteDir := "disk:/remote"
	content := "same content"

	p := writeLocal(t, localDir, "file.txt", content)
	info, _ := os.Stat(p)

	mock := NewMockClient()
	addRemoteFile(mock, remoteDir+"/file.txt", content, info.ModTime())

	s := newTestSyncer(t, testConfig(t), mock)
	result := s.BidirectionalSync(context.Background(), localDir, remoteDir, "bidirectional", "none")

	if len(result.Uploaded) != 0 || len(result.Downloaded) != 0 {
		t.Errorf("Identical file should not be transferred: up=%v down=%v", result.Uploaded, result.Downloaded)
	}
	if len(result.Skipped) != 1 {
		t.Errorf("Expected 1 skipped, got %d", len(result.Skipped))
	}
}

func TestSync_ConflictLocalNewer_Uploads(t *testing.T) {
	localDir := filepath.Join(t.TempDir(), "local")
	remoteDir := "disk:/remote"

	p := writeLocal(t, localDir, "conflict.txt", "local version")
	future := time.Now().Add(time.Hour)
	_ = os.Chtimes(p, future, future)

	mock := NewMockClient()
	addRemoteFile(mock, remoteDir+"/conflict.txt", "remote version", time.Now().Add(-time.Hour))

	s := newTestSyncer(t, testConfig(t), mock)
	result := s.BidirectionalSync(context.Background(), localDir, remoteDir, "bidirectional", "none")

	if len(result.Uploaded) != 1 {
		t.Errorf("Local-newer conflict should upload, got up=%v down=%v", result.Uploaded, result.Downloaded)
	}
	if len(result.Downloaded) != 0 {
		t.Error("File must not be transferred in both directions")
	}
}

func TestSync_ConflictRemoteNewer_Downloads(t *testing.T) {
	localDir := filepath.Join(t.TempDir(), "local")
	remoteDir := "disk:/remote"

	p := writeLocal(t, localDir, "conflict.txt", "local version")
	past := time.Now().Add(-time.Hour)
	_ = os.Chtimes(p, past, past)

	mock := NewMockClient()
	addRemoteFile(mock, remoteDir+"/conflict.txt", "remote version", time.Now())

	s := newTestSyncer(t, testConfig(t), mock)
	result := s.BidirectionalSync(context.Background(), localDir, remoteDir, "bidirectional", "none")

	if len(result.Downloaded) != 1 {
		t.Errorf("Remote-newer conflict should download, got up=%v down=%v", result.Uploaded, result.Downloaded)
	}
	if len(result.Uploaded) != 0 {
		t.Error("File must not be transferred in both directions")
	}
}

func TestSync_ConflictEqualMtime_RemoteWins(t *testing.T) {
	localDir := filepath.Join(t.TempDir(), "local")
	remoteDir := "disk:/remote"

	ts := time.Now().Truncate(time.Second)
	p := writeLocal(t, localDir, "conflict.txt", "local version")
	_ = os.Chtimes(p, ts, ts)

	mock := NewMockClient()
	addRemoteFile(mock, remoteDir+"/conflict.txt", "remote version", ts)

	s := newTestSyncer(t, testConfig(t), mock)
	result := s.BidirectionalSync(context.Background(), localDir, remoteDir, "bidirectional", "none")

	if len(result.Downloaded) != 1 || len(result.Uploaded) != 0 {
		t.Errorf("Tie on mtime: remote should win (download), got up=%v down=%v", result.Uploaded, result.Downloaded)
	}
}

func TestSync_DownloadOnlyMode(t *testing.T) {
	localDir := filepath.Join(t.TempDir(), "local")
	remoteDir := "disk:/remote"

	writeLocal(t, localDir, "local-only.txt", "local data")
	mock := NewMockClient()
	addRemoteFile(mock, remoteDir+"/remote-only.txt", "remote data", time.Now())

	s := newTestSyncer(t, testConfig(t), mock)
	result := s.BidirectionalSync(context.Background(), localDir, remoteDir, "download", "none")

	if len(result.Downloaded) != 1 {
		t.Errorf("Expected 1 download, got %v", result.Downloaded)
	}
	if len(result.Uploaded) != 0 {
		t.Errorf("download mode must not upload, got %v", result.Uploaded)
	}
	if len(result.DeletedLocal) != 0 {
		t.Errorf("delete none must not delete, got %v", result.DeletedLocal)
	}
}

func TestSync_DownloadMode_DeleteLocalUnsynced(t *testing.T) {
	localDir := filepath.Join(t.TempDir(), "local")
	remoteDir := "disk:/remote"

	writeLocal(t, localDir, "unsynced.txt", "never synced")
	mock := NewMockClient()

	s := newTestSyncer(t, testConfig(t), mock)
	result := s.BidirectionalSync(context.Background(), localDir, remoteDir, "download", "local")

	if len(result.DeletedLocal) != 1 {
		t.Errorf("download+delete local should remove unsynced local file, got %v", result.DeletedLocal)
	}
	if _, err := os.Stat(filepath.Join(localDir, "unsynced.txt")); !os.IsNotExist(err) {
		t.Error("local file should be deleted")
	}
}

func TestSync_RemoteDeletionPropagates(t *testing.T) {
	localDir := filepath.Join(t.TempDir(), "local")
	remoteDir := "disk:/remote"

	writeLocal(t, localDir, "victim.txt", "synced content")
	mock := NewMockClient()
	s := newTestSyncer(t, testConfig(t), mock)
	ctx := context.Background()

	// Прогон 1: файл загружается и помечается синхронизированным
	r1 := s.BidirectionalSync(ctx, localDir, remoteDir, "bidirectional", "none")
	if len(r1.Uploaded) != 1 {
		t.Fatalf("first run should upload, got %v", r1.Uploaded)
	}

	// Удаляем файл на «удалённой» стороне
	mock.mu.Lock()
	delete(mock.Files, remoteDir+"/victim.txt")
	delete(mock.Contents, remoteDir+"/victim.txt")
	mock.mu.Unlock()

	// Прогон 2: удаление должно распространиться на локальную сторону
	r2 := s.BidirectionalSync(ctx, localDir, remoteDir, "bidirectional", "both")
	if len(r2.DeletedLocal) != 1 {
		t.Errorf("remote deletion should propagate locally, got deleted=%v errors=%v", r2.DeletedLocal, r2.Errors)
	}
	if _, err := os.Stat(filepath.Join(localDir, "victim.txt")); !os.IsNotExist(err) {
		t.Error("local file should be deleted after remote deletion")
	}
}

func TestSync_RemoteDeletion_NoDeleteMode_Reuploads(t *testing.T) {
	localDir := filepath.Join(t.TempDir(), "local")
	remoteDir := "disk:/remote"

	writeLocal(t, localDir, "victim.txt", "synced content")
	mock := NewMockClient()
	s := newTestSyncer(t, testConfig(t), mock)
	ctx := context.Background()

	s.BidirectionalSync(ctx, localDir, remoteDir, "bidirectional", "none")

	mock.mu.Lock()
	delete(mock.Files, remoteDir+"/victim.txt")
	delete(mock.Contents, remoteDir+"/victim.txt")
	mock.mu.Unlock()

	// delete_mode=none → файл восстанавливается, а не удаляется
	r2 := s.BidirectionalSync(ctx, localDir, remoteDir, "bidirectional", "none")
	if len(r2.DeletedLocal) != 0 {
		t.Errorf("delete none must not delete, got %v", r2.DeletedLocal)
	}
	if len(r2.Uploaded) != 1 {
		t.Errorf("file should be re-uploaded (restored), got %v", r2.Uploaded)
	}
}

func TestSync_LocalDeletionPropagates(t *testing.T) {
	localDir := filepath.Join(t.TempDir(), "local")
	remoteDir := "disk:/remote"

	localFile := writeLocal(t, localDir, "victim.txt", "synced content")
	mock := NewMockClient()
	s := newTestSyncer(t, testConfig(t), mock)
	ctx := context.Background()

	s.BidirectionalSync(ctx, localDir, remoteDir, "bidirectional", "none")

	if err := os.Remove(localFile); err != nil {
		t.Fatal(err)
	}

	r2 := s.BidirectionalSync(ctx, localDir, remoteDir, "bidirectional", "both")
	if len(r2.DeletedRemote) != 1 {
		t.Errorf("local deletion should propagate to remote, got deleted=%v errors=%v", r2.DeletedRemote, r2.Errors)
	}
	if _, ok := mock.Files[remoteDir+"/victim.txt"]; ok {
		t.Error("remote file should be deleted after local deletion")
	}
}

func TestSync_UploadMode_DeleteRemoteUnsynced(t *testing.T) {
	localDir := filepath.Join(t.TempDir(), "local")
	remoteDir := "disk:/remote"

	writeLocal(t, localDir, "kept.txt", "data")
	mock := NewMockClient()
	addRemoteFile(mock, remoteDir+"/stray.txt", "remote junk", time.Now())

	s := newTestSyncer(t, testConfig(t), mock)
	result := s.BidirectionalSync(context.Background(), localDir, remoteDir, "upload", "remote")

	if len(result.DeletedRemote) != 1 || mock.Deletes[0] != remoteDir+"/stray.txt" {
		t.Errorf("upload+delete remote should remove stray remote file, got %v", result.DeletedRemote)
	}
	if len(result.Downloaded) != 0 {
		t.Errorf("upload mode must not download, got %v", result.Downloaded)
	}
}

func TestSync_NoDoubleTransferOnConflict(t *testing.T) {
	// Регрессия на старый баг: файл с разным содержимым передавался в обе стороны
	localDir := filepath.Join(t.TempDir(), "local")
	remoteDir := "disk:/remote"

	writeLocal(t, localDir, "both.txt", "local version")
	mock := NewMockClient()
	addRemoteFile(mock, remoteDir+"/both.txt", "remote version", time.Now().Add(time.Hour))

	s := newTestSyncer(t, testConfig(t), mock)
	result := s.BidirectionalSync(context.Background(), localDir, remoteDir, "bidirectional", "none")

	total := len(result.Uploaded) + len(result.Downloaded)
	if total != 1 {
		t.Errorf("conflicting file must transfer exactly once, got up=%v down=%v", result.Uploaded, result.Downloaded)
	}
}

func TestSync_ExcludePatterns(t *testing.T) {
	localDir := t.TempDir()
	remoteDir := "disk:/test"

	writeLocal(t, localDir, "keep.txt", "keep me")
	writeLocal(t, localDir, "node_modules/pkg/index.js", "module code")
	writeLocal(t, localDir, "deep/nested/node_modules/inner.js", "nested module")
	writeLocal(t, localDir, "cache_session.dat", "cache")
	writeLocal(t, localDir, "notes.tmp", "temp")
	writeLocal(t, localDir, "notes.txt", "not temp")

	mock := &MockClient{
		Files:    map[string]*models.Resource{},
		Contents: map[string][]byte{},
		Folders:  map[string]bool{"disk:/": true, remoteDir: true},
	}
	// Удалённый файл в исключаемой папке — не должен ни скачиваться, ни удаляться
	addRemoteFile(mock, remoteDir+"/vendor/lib.so", "lib", time.Now())
	// Удалённый файл вне исключений — скачается
	addRemoteFile(mock, remoteDir+"/remote.txt", "remote", time.Now())

	cfg := testConfig(t, config.FolderMapping{Local: localDir, Remote: remoteDir})
	s := newTestSyncer(t, cfg, mock)

	exclude := []string{"node_modules", "cache_*", "*.tmp", "vendor"}
	result := s.SyncFolderWithExcludes(context.Background(), localDir, remoteDir, "bidirectional", "both", exclude)

	// Загружены только неисключённые локальные файлы
	if len(result.Uploaded) != 2 {
		t.Errorf("Expected 2 uploads (keep.txt, notes.txt), got %v", result.Uploaded)
	}
	if len(result.Downloaded) != 1 || result.Downloaded[0] != "remote.txt" {
		t.Errorf("Expected 1 download (remote.txt), got %v", result.Downloaded)
	}
	// vendor/lib.so не удалён в облаке
	if len(result.DeletedRemote) != 0 {
		t.Errorf("Expected no remote deletions (vendor excluded), got %v", result.DeletedRemote)
	}
	if len(result.DeletedLocal) != 0 {
		t.Errorf("Expected no local deletions, got %v", result.DeletedLocal)
	}
}
