package yandex

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Client реализует ClientInterface
type Client struct {
	token   string
	baseURL string
	client  *http.Client
	timeout time.Duration
}

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

// NewClient создает новый клиент Яндекс.Диска
func NewClient(token string) *Client {
	return &Client{
		token:   token,
		baseURL: "https://cloud-api.yandex.net/v1/disk",
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				IdleConnTimeout:     30 * time.Second,
				DisableCompression:  false,
				DisableKeepAlives:   false,
				MaxIdleConnsPerHost: 10,
			},
		},
		timeout: 30 * time.Second,
	}
}

// SetBaseURL устанавливает базовый URL (для тестов)
func (c *Client) SetBaseURL(url string) {
	c.baseURL = url
}

// SetTimeout устанавливает таймаут для запросов
func (c *Client) SetTimeout(timeout time.Duration) {
	c.timeout = timeout
	c.client.Timeout = timeout
}

func (c *Client) doRequest(method, endpoint string, queryParams map[string]string, body io.Reader) (*http.Response, error) {
	// Построение URL
	u, err := url.Parse(c.baseURL + endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	// Добавление query параметров
	if queryParams != nil {
		q := u.Query()
		for key, value := range queryParams {
			q.Set(key, value)
		}
		u.RawQuery = q.Encode()
	}

	// Создание запроса
	req, err := http.NewRequest(method, u.String(), body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Установка заголовков
	req.Header.Set("Authorization", "OAuth "+c.token)
	if body != nil && method != "GET" && method != "DELETE" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Yandex-Disk-Sync/1.0")

	// Выполнение запроса
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

func (c *Client) GetDiskInfo() (*DiskInfo, error) {
	resp, err := c.doRequest("GET", "/", nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Чтение тела ответа
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Проверка статуса
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	// Парсинг JSON
	var diskInfo DiskInfo
	err = json.Unmarshal(body, &diskInfo)
	if err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return &diskInfo, nil
}

func (c *Client) CreateFolder(remotePath string) error {
	params := map[string]string{
		"path": remotePath,
	}

	resp, err := c.doRequest("PUT", "/resources", params, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Чтение тела ответа для отладки
	body, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusCreated:
		return nil
	case http.StatusConflict:
		// Папка уже существует - это не ошибка
		return nil
	default:
		return fmt.Errorf("failed to create folder %s: %d %s",
			remotePath, resp.StatusCode, string(body))
	}
}

func (c *Client) GetResource(remotePath string) (*Resource, error) {
	params := map[string]string{
		"path":   remotePath,
		"fields": "path,type,name,size,md5,created,modified,mime_type,custom_properties",
	}

	resp, err := c.doRequest("GET", "/resources", params, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Чтение тела ответа
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var resource Resource
	err = json.Unmarshal(body, &resource)
	if err != nil {
		return nil, fmt.Errorf("failed to parse resource: %w", err)
	}

	return &resource, nil
}

func (c *Client) ListFolderResources(remotePath string, limit, offset int, fields string) (*ResourceList, error) {
	params := map[string]string{
		"path":   remotePath,
		"fields": fields,
		"limit":  fmt.Sprintf("%d", limit),
		"offset": fmt.Sprintf("%d", offset),
	}

	resp, err := c.doRequest("GET", "/resources", params, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Чтение тела ответа
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return &ResourceList{Items: []Resource{}}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var resource Resource
	err = json.Unmarshal(body, &resource)
	if err != nil {
		return nil, fmt.Errorf("failed to parse resource list: %w", err)
	}

	if resource.Embedded == nil {
		return &ResourceList{Items: []Resource{}}, nil
	}

	return resource.Embedded, nil
}

func (c *Client) GetAllResources(remotePath string) ([]Resource, error) {
	var allResources []Resource
	limit := 1000 // максимальный лимит API
	offset := 0
	fields := "path,type,name,size,md5,created,modified,mime_type,_embedded.items"

	for {
		resourceList, err := c.ListFolderResources(remotePath, limit, offset, fields)
		if err != nil {
			return nil, err
		}

		for _, item := range resourceList.Items {
			allResources = append(allResources, item)
			if item.Type == "dir" {
				subResources, err := c.GetAllResources(item.Path)
				if err != nil {
					return nil, err
				}
				allResources = append(allResources, subResources...)
			}
		}

		if len(resourceList.Items) < limit || offset+len(resourceList.Items) >= resourceList.Total {
			break
		}
		offset += len(resourceList.Items)
	}

	return allResources, nil
}

func (c *Client) GetResourcesByPath(remotePath string) ([]Resource, error) {
	return c.GetAllResources(remotePath)
}

func (c *Client) UploadFile(localPath, remotePath string) error {
	// 1. Получение URL для загрузки
	params := map[string]string{
		"path":      remotePath,
		"overwrite": "true",
	}

	resp, err := c.doRequest("GET", "/resources/upload", params, nil)
	if err != nil {
		return fmt.Errorf("failed to get upload URL: %w", err)
	}
	defer resp.Body.Close()

	// Чтение тела ответа
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read upload URL response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to get upload URL %d: %s", resp.StatusCode, string(body))
	}

	var uploadLink UploadLink
	err = json.Unmarshal(body, &uploadLink)
	if err != nil {
		return fmt.Errorf("failed to parse upload link: %w", err)
	}

	// 2. Чтение локального файла
	file, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open local file: %w", err)
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to get file info: %w", err)
	}

	// 3. Загрузка файла
	req, err := http.NewRequest(uploadLink.Method, uploadLink.Href, file)
	if err != nil {
		return fmt.Errorf("failed to create upload request: %w", err)
	}

	req.ContentLength = fileInfo.Size()
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Length", fmt.Sprintf("%d", fileInfo.Size()))

	uploadResp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("upload request failed: %w", err)
	}
	defer uploadResp.Body.Close()

	// Чтение тела ответа загрузки
	uploadBody, _ := io.ReadAll(uploadResp.Body)

	if uploadResp.StatusCode != http.StatusCreated {
		return fmt.Errorf("upload failed %d: %s", uploadResp.StatusCode, string(uploadBody))
	}

	return nil
}

func (c *Client) DownloadFile(remotePath, localPath string) error {
	// 1. Получение URL для скачивания
	params := map[string]string{
		"path": remotePath,
	}

	resp, err := c.doRequest("GET", "/resources/download", params, nil)
	if err != nil {
		return fmt.Errorf("failed to get download URL: %w", err)
	}
	defer resp.Body.Close()

	// Чтение тела ответа
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read download URL response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to get download URL %d: %s", resp.StatusCode, string(body))
	}

	var downloadLink DownloadLink
	err = json.Unmarshal(body, &downloadLink)
	if err != nil {
		return fmt.Errorf("failed to parse download link: %w", err)
	}

	// 2. Создание локальной директории
	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create local directory: %w", err)
	}

	// 3. Скачивание файла
	req, err := http.NewRequest("GET", downloadLink.Href, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}

	req.Header.Set("Authorization", "OAuth "+c.token)

	downloadResp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("download request failed: %w", err)
	}
	defer downloadResp.Body.Close()

	if downloadResp.StatusCode != http.StatusOK {
		downloadBody, _ := io.ReadAll(downloadResp.Body)
		return fmt.Errorf("download failed %d: %s", downloadResp.StatusCode, string(downloadBody))
	}

	// 4. Сохранение в локальный файл
	file, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer file.Close()

	_, err = io.Copy(file, downloadResp.Body)
	if err != nil {
		return fmt.Errorf("failed to save downloaded file: %w", err)
	}

	return nil
}

func (c *Client) DeleteResource(remotePath string, permanently bool) error {
	params := map[string]string{
		"path": remotePath,
	}

	if permanently {
		params["permanently"] = "true"
	}

	resp, err := c.doRequest("DELETE", "/resources", params, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Чтение тела ответа
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("failed to delete resource %s: %d %s",
			remotePath, resp.StatusCode, string(body))
	}

	return nil
}

func (c *Client) EnsureFolderExists(remotePath string) error {
	// Удаление префикса "disk:/" и очистка пути
	cleanPath := strings.TrimPrefix(remotePath, "disk:/")
	cleanPath = strings.TrimPrefix(cleanPath, "/")

	if cleanPath == "" {
		return nil // Корневая папка всегда существует
	}

	// Рекурсивное создание структуры папок
	parts := strings.Split(cleanPath, "/")
	currentPath := ""

	for _, part := range parts {
		if part == "" {
			continue
		}
		if currentPath == "" {
			currentPath = part
		} else {
			currentPath = path.Join(currentPath, part)
		}

		fullPath := "disk:/" + currentPath
		resource, err := c.GetResource(fullPath)
		if err != nil {
			return fmt.Errorf("failed to check folder %s: %w", fullPath, err)
		}

		if resource == nil {
			err = c.CreateFolder(fullPath)
			if err != nil {
				return fmt.Errorf("failed to create folder %s: %w", fullPath, err)
			}
		} else if resource.Type != "dir" {
			return fmt.Errorf("resource exists but is not a folder: %s", fullPath)
		}
	}

	return nil
}

func (c *Client) GetResourceMetadata(remotePath string, fields []string) (*Resource, error) {
	fieldsStr := strings.Join(fields, ",")
	params := map[string]string{
		"path":   remotePath,
		"fields": fieldsStr,
	}

	resp, err := c.doRequest("GET", "/resources", params, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	// Чтение тела ответа
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var resource Resource
	err = json.Unmarshal(body, &resource)
	if err != nil {
		return nil, fmt.Errorf("failed to parse resource metadata: %w", err)
	}

	return &resource, nil
}

func (c *Client) CheckOperationStatus(operationURL string) (string, error) {
	req, err := http.NewRequest("GET", operationURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create status request: %w", err)
	}

	req.Header.Set("Authorization", "OAuth "+c.token)
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("status request failed: %w", err)
	}
	defer resp.Body.Close()

	// Чтение тела ответа
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read status response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status check failed %d: %s", resp.StatusCode, string(body))
	}

	var operation Operation
	err = json.Unmarshal(body, &operation)
	if err != nil {
		return "", fmt.Errorf("failed to parse operation status: %w", err)
	}

	return operation.Status, nil
}
