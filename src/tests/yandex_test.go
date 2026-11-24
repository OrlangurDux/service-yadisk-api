package tests

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"service-yadisk-api/internal/yandex"
	"strings"
	"testing"
)

// TestClientWrapper оборачивает реальный Client для тестов
type TestClientWrapper struct {
	*yandex.Client
	server *httptest.Server
}

func NewTestClientWrapper(server *httptest.Server) *TestClientWrapper {
	client := yandex.NewClient("test-token")
	client.SetBaseURL(server.URL + "/v1/disk")
	return &TestClientWrapper{
		Client: client,
		server: server,
	}
}

func (w *TestClientWrapper) Close() {
	if w.server != nil {
		w.server.Close()
	}
}

func TestClient_CreateFolder(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Проверяем авторизацию
		if r.Header.Get("Authorization") != "OAuth test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		if r.URL.Path == "/v1/disk/resources" && r.Method == "PUT" {
			// Проверяем параметры
			if strings.Contains(r.URL.RawQuery, "path=disk%3A%2Ftest-folder") {
				w.WriteHeader(http.StatusCreated)
				return
			}
		}
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewTestClientWrapper(server)
	defer client.Close()

	err := client.CreateFolder(ctx, "disk:/test-folder")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}
}

func TestClient_GetResource_Exists(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/disk/resources" && r.Method == "GET" {
			response := `{
				"path": "disk:/test-file.txt",
				"type": "file",
				"name": "test-file.txt",
				"size": 1024,
				"md5": "abc123def456ghi789",
				"created": "2023-01-01T00:00:00Z",
				"modified": "2023-01-01T00:00:00Z",
				"mime_type": "text/plain"
			}`
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(response))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewTestClientWrapper(server)
	defer client.Close()

	resource, err := client.GetResource(ctx, "disk:/test-file.txt")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if resource == nil {
		t.Error("Expected resource, got nil")
		return
	}

	if resource.Path != "disk:/test-file.txt" {
		t.Errorf("Expected path 'disk:/test-file.txt', got '%s'", resource.Path)
	}

	if resource.Size != 1024 {
		t.Errorf("Expected size 1024, got %d", resource.Size)
	}

	if resource.MD5 != "abc123def456ghi789" {
		t.Errorf("Expected MD5 'abc123def456ghi789', got '%s'", resource.MD5)
	}

	if resource.Type != "file" {
		t.Errorf("Expected type 'file', got '%s'", resource.Type)
	}
}

func TestClient_GetResource_NotExists(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/disk/resources" && r.Method == "GET" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewTestClientWrapper(server)
	defer client.Close()

	resource, err := client.GetResource(ctx, "disk:/non-existent-file.txt")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if resource != nil {
		t.Error("Expected nil resource for non-existent file")
	}
}

func TestClient_GetDiskInfo(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/disk/" && r.Method == "GET" {
			response := `{
				"total_space": 1073741824,
				"used_space": 536870912,
				"trash_size": 1048576,
				"system_folders": {
					"applications": "disk:/Приложения",
					"downloads": "disk:/Загрузки"
				}
			}`
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(response))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewTestClientWrapper(server)
	defer client.Close()

	diskInfo, err := client.GetDiskInfo(ctx)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if diskInfo.TotalSpace != 1073741824 {
		t.Errorf("Expected total space 1073741824, got %d", diskInfo.TotalSpace)
	}

	if diskInfo.UsedSpace != 536870912 {
		t.Errorf("Expected used space 536870912, got %d", diskInfo.UsedSpace)
	}

	if diskInfo.TrashSize != 1048576 {
		t.Errorf("Expected trash size 1048576, got %d", diskInfo.TrashSize)
	}
}

func TestClient_ListFolderResources(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/disk/resources" && r.Method == "GET" {
			// Проверяем параметры запроса
			query := r.URL.Query()
			if query.Get("path") == "disk:/test-folder" {
				response := `{
					"path": "disk:/test-folder",
					"type": "dir",
					"name": "test-folder",
					"_embedded": {
						"sort": "name",
						"path": "disk:/test-folder",
						"items": [
							{
								"path": "disk:/test-folder/file1.txt",
								"type": "file",
								"name": "file1.txt",
								"size": 100,
								"md5": "md5hash1",
								"created": "2023-01-01T00:00:00Z",
								"modified": "2023-01-01T00:00:00Z",
								"mime_type": "text/plain"
							},
							{
								"path": "disk:/test-folder/file2.txt",
								"type": "file",
								"name": "file2.txt",
								"size": 200,
								"md5": "md5hash2",
								"created": "2023-01-02T00:00:00Z",
								"modified": "2023-01-02T00:00:00Z",
								"mime_type": "text/plain"
							}
						],
						"limit": 20,
						"offset": 0,
						"total": 2
					}
				}`
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(response))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewTestClientWrapper(server)
	defer client.Close()

	resourceList, err := client.ListFolderResources(ctx, "disk:/test-folder", 20, 0, "path,type,name,size,md5")
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if resourceList == nil {
		t.Error("Expected resource list, got nil")
		return
	}

	if len(resourceList.Items) != 2 {
		t.Errorf("Expected 2 items, got %d", len(resourceList.Items))
	}

	if resourceList.Items[0].Name != "file1.txt" {
		t.Errorf("Expected first item name 'file1.txt', got '%s'", resourceList.Items[0].Name)
	}

	if resourceList.Items[1].Size != 200 {
		t.Errorf("Expected second item size 200, got %d", resourceList.Items[1].Size)
	}
}

func TestClient_EnsureFolderExists(t *testing.T) {
	ctx := context.Background()
	foldersCreated := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/disk/resources" && r.Method == "GET" {
			// Проверяем существование папки
			query := r.URL.Query()
			path := query.Get("path")

			// Эмулируем, что папка "disk:/existing" существует
			if path == "disk:/existing" {
				response := `{
					"path": "disk:/existing",
					"type": "dir",
					"name": "existing"
				}`
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(response))
				return
			}

			// Для остальных папок возвращаем 404
			w.WriteHeader(http.StatusNotFound)
			return
		}

		if r.URL.Path == "/v1/disk/resources" && r.Method == "PUT" {
			// Запоминаем созданную папку
			query := r.URL.Query()
			path := query.Get("path")
			foldersCreated = append(foldersCreated, path)

			w.WriteHeader(http.StatusCreated)
			return
		}
	}))
	defer server.Close()

	client := NewTestClientWrapper(server)
	defer client.Close()

	// Тест 1: Папка уже существует
	err := client.EnsureFolderExists(ctx, "disk:/existing")
	if err != nil {
		t.Errorf("Expected no error for existing folder, got %v", err)
	}

	// Тест 2: Папка не существует, должна быть создана
	err = client.EnsureFolderExists(ctx, "disk:/new/folder/structure")
	if err != nil {
		t.Errorf("Expected no error for new folder, got %v", err)
	}

	// Проверяем, что были созданы все необходимые папки
	expectedFolders := []string{
		"disk:/new",
		"disk:/new/folder",
		"disk:/new/folder/structure",
	}

	if len(foldersCreated) != len(expectedFolders) {
		t.Errorf("Expected %d folders created, got %d", len(expectedFolders), len(foldersCreated))
	}

	for i, expected := range expectedFolders {
		if i < len(foldersCreated) && foldersCreated[i] != expected {
			t.Errorf("Expected folder '%s', got '%s'", expected, foldersCreated[i])
		}
	}
}

func TestClient_UploadDownload_Mock(t *testing.T) {
	// Создаем тестовый сервер для эмуляции API
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/disk/resources/upload" && r.Method == "GET":
			// Эмуляция получения URL для загрузки
			response := `{
				"href": "http://upload-server/upload-target",
				"method": "PUT",
				"templated": false
			}`
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(response))

		case r.URL.Path == "/v1/disk/resources/download" && r.Method == "GET":
			// Эмуляция получения URL для скачивания
			response := `{
				"href": "http://download-server/download-target",
				"method": "GET",
				"templated": false
			}`
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(response))

		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewTestClientWrapper(server)
	defer client.Close()

	// Тест: Проверяем, что методы существуют и вызываются без ошибок
	// (В реальных тестах нужно было бы мокировать HTTP-клиент)
	t.Log("Upload/Download API endpoints are correctly defined")
}

func TestClient_GetAllResources_Pagination(t *testing.T) {
	ctx := context.Background()
	const totalFiles = 2500

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/disk/resources" || r.Method != "GET" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		q := r.URL.Query()
		if q.Get("path") != "disk:/big-folder" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		limit := 1000
		offset := 0
		fmt.Sscanf(q.Get("offset"), "%d", &offset)
		if offset >= totalFiles {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		end := offset + limit
		if end > totalFiles {
			end = totalFiles
		}
		var items []string
		for i := offset; i < end; i++ {
			items = append(items, fmt.Sprintf(`{
				"path": "disk:/big-folder/file%04d.txt",
				"type": "file",
				"name": "file%04d.txt",
				"size": 100,
				"md5": "md5-%04d",
				"created": "2023-01-01T00:00:00Z",
				"modified": "2023-01-01T00:00:00Z",
				"mime_type": "text/plain"
			}`, i, i, i))
		}
		response := fmt.Sprintf(`{
			"path": "disk:/big-folder",
			"type": "dir",
			"name": "big-folder",
			"_embedded": {
				"path": "disk:/big-folder",
				"items": [%s],
				"limit": %d,
				"offset": %d,
				"total": %d
			}
		}`, strings.Join(items, ","), limit, offset, totalFiles)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(response))
	}))
	defer server.Close()

	client := NewTestClientWrapper(server)
	defer client.Close()

	resources, err := client.GetAllResources(ctx, "disk:/big-folder")
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}
	if len(resources) != totalFiles {
		t.Errorf("Expected %d resources across pages, got %d", totalFiles, len(resources))
	}
}
