package yandex

import (
	"context"
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

	"service-yadisk-api/internal/models"
)

const (
	maxRetries   = 3
	retryBackoff = 500 * time.Millisecond
)

// Client реализует ClientInterface
type Client struct {
	token    string
	baseURL  string
	client   *http.Client // запросы к API
	transfer *http.Client // передача тел файлов на storage-ноды Яндекса
}

// NewClient создает новый клиент Яндекс.Диска
func NewClient(token string) *Client {
	return &Client{
		token:   token,
		baseURL: "https://cloud-api.yandex.net/v1/disk",
		client: &http.Client{
			Timeout: 960 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				IdleConnTimeout:     960 * time.Second,
				DisableCompression:  false,
				DisableKeepAlives:   false,
				MaxIdleConnsPerHost: 10,
			},
		},
		// Без общего Timeout: тело большого файла льётся столько, сколько
		// требует канал. ResponseHeaderTimeout ловит зависший сервер после
		// полной отправки запроса; отмена — через ctx.
		transfer: &http.Client{
			Timeout: 0,
			Transport: &http.Transport{
				MaxIdleConns:          10,
				IdleConnTimeout:       120 * time.Second,
				ResponseHeaderTimeout: 120 * time.Second,
				MaxIdleConnsPerHost:   10,
			},
		},
	}
}

// SetBaseURL устанавливает базовый URL (для тестов)
func (c *Client) SetBaseURL(url string) {
	c.baseURL = url
}

// SetTimeout устанавливает таймаут для запросов
func (c *Client) SetTimeout(timeout time.Duration) {
	c.client.Timeout = timeout
}

// isRetryableStatus — временные ошибки API, при которых имеет смысл повтор
func isRetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

// sleepWithContext ждёт d или отмену ctx
func sleepWithContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// doRequest выполняет HTTP-запрос к API с retry для временных ошибок
// (сетевые сбои, 429, 5xx). Тело ответа читается вызывающим кодом.
func (c *Client) doRequest(ctx context.Context, method, endpoint string, queryParams map[string]string, body io.Reader) (*http.Response, error) {
	u, err := url.Parse(c.baseURL + endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	if queryParams != nil {
		q := u.Query()
		for key, value := range queryParams {
			q.Set(key, value)
		}
		u.RawQuery = q.Encode()
	}

	var resp *http.Response
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepWithContext(ctx, retryBackoff*time.Duration(1<<(attempt-1))); err != nil {
				return nil, err
			}
		}

		req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}
		req.Header.Set("Authorization", "OAuth "+c.token)
		if body != nil && method != "GET" && method != "DELETE" {
			req.Header.Set("Content-Type", "application/json")
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "Yandex-Disk-Sync/1.0")

		resp, err = c.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}

		if isRetryableStatus(resp.StatusCode) {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			lastErr = fmt.Errorf("API error %d (retryable)", resp.StatusCode)
			continue
		}

		return resp, nil
	}
	return nil, lastErr
}

func (c *Client) GetDiskInfo(ctx context.Context) (*models.DiskInfo, error) {
	resp, err := c.doRequest(ctx, "GET", "/", nil, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var diskInfo models.DiskInfo
	if err := json.Unmarshal(body, &diskInfo); err != nil {
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	return &diskInfo, nil
}

func (c *Client) CreateFolder(ctx context.Context, remotePath string) error {
	params := map[string]string{
		"path": remotePath,
	}

	resp, err := c.doRequest(ctx, "PUT", "/resources", params, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

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

func (c *Client) GetResource(ctx context.Context, remotePath string) (*models.Resource, error) {
	params := map[string]string{
		"path":   remotePath,
		"fields": "path,type,name,size,md5,created,modified,mime_type,custom_properties",
	}

	resp, err := c.doRequest(ctx, "GET", "/resources", params, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

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

	var resource models.Resource
	if err := json.Unmarshal(body, &resource); err != nil {
		return nil, fmt.Errorf("failed to parse resource: %w", err)
	}

	return &resource, nil
}

func (c *Client) ListFolderResources(ctx context.Context, remotePath string, limit, offset int, fields string) (*models.ResourceList, error) {
	params := map[string]string{
		"path":   remotePath,
		"fields": fields,
		"limit":  fmt.Sprintf("%d", limit),
		"offset": fmt.Sprintf("%d", offset),
	}

	resp, err := c.doRequest(ctx, "GET", "/resources", params, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode == http.StatusNotFound {
		return &models.ResourceList{Items: []models.Resource{}}, nil
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(body))
	}

	var resource models.Resource
	if err := json.Unmarshal(body, &resource); err != nil {
		return nil, fmt.Errorf("failed to parse resource list: %w", err)
	}

	if resource.Embedded == nil {
		return &models.ResourceList{Items: []models.Resource{}}, nil
	}

	return resource.Embedded, nil
}

func (c *Client) GetAllResources(ctx context.Context, remotePath string) ([]models.Resource, error) {
	var allResources []models.Resource
	limit := 1000 // максимальный лимит API
	offset := 0
	// В fields обязателен _embedded.total — без него API вернёт total=0 и
	// пагинация оборвётся после первой страницы (максимум 1000 файлов).
	fields := "path,type,name,size,md5,created,modified,mime_type,_embedded.total,_embedded.items"

	for {
		resourceList, err := c.ListFolderResources(ctx, remotePath, limit, offset, fields)
		if err != nil {
			return nil, err
		}

		for _, item := range resourceList.Items {
			allResources = append(allResources, item)
			if item.Type == "dir" {
				subResources, err := c.GetAllResources(ctx, item.Path)
				if err != nil {
					return nil, err
				}
				allResources = append(allResources, subResources...)
			}
		}

		offset += len(resourceList.Items)
		if len(resourceList.Items) < limit {
			break
		}
		if resourceList.Total > 0 && offset >= resourceList.Total {
			break
		}
	}

	return allResources, nil
}

func (c *Client) GetResourcesByPath(ctx context.Context, remotePath string) ([]models.Resource, error) {
	return c.GetAllResources(ctx, remotePath)
}

func (c *Client) UploadFile(ctx context.Context, localPath, remotePath string) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepWithContext(ctx, retryBackoff*time.Duration(1<<(attempt-1))); err != nil {
				return err
			}
		}
		if lastErr = c.uploadFileOnce(ctx, localPath, remotePath); lastErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return lastErr
}

func (c *Client) uploadFileOnce(ctx context.Context, localPath, remotePath string) error {
	// 1. Получение URL для загрузки
	params := map[string]string{
		"path":      remotePath,
		"overwrite": "true",
	}

	resp, err := c.doRequest(ctx, "GET", "/resources/upload", params, nil)
	if err != nil {
		return fmt.Errorf("failed to get upload URL: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read upload URL response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to get upload URL %d: %s", resp.StatusCode, string(body))
	}

	var uploadLink models.UploadLink
	if err := json.Unmarshal(body, &uploadLink); err != nil {
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
	req, err := http.NewRequestWithContext(ctx, uploadLink.Method, uploadLink.Href, file)
	if err != nil {
		return fmt.Errorf("failed to create upload request: %w", err)
	}

	req.ContentLength = fileInfo.Size()
	req.Header.Set("Content-Type", "application/octet-stream")

	uploadResp, err := c.transfer.Do(req)
	if err != nil {
		return fmt.Errorf("upload request failed: %w", err)
	}
	defer uploadResp.Body.Close()

	uploadBody, _ := io.ReadAll(uploadResp.Body)

	if uploadResp.StatusCode != http.StatusCreated {
		return fmt.Errorf("upload failed %d: %s", uploadResp.StatusCode, string(uploadBody))
	}

	return nil
}

func (c *Client) DownloadFile(ctx context.Context, remotePath, localPath string) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			if err := sleepWithContext(ctx, retryBackoff*time.Duration(1<<(attempt-1))); err != nil {
				return err
			}
		}
		if lastErr = c.downloadFileOnce(ctx, remotePath, localPath); lastErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return lastErr
}

func (c *Client) downloadFileOnce(ctx context.Context, remotePath, localPath string) error {
	// 1. Получение URL для скачивания
	params := map[string]string{
		"path": remotePath,
	}

	resp, err := c.doRequest(ctx, "GET", "/resources/download", params, nil)
	if err != nil {
		return fmt.Errorf("failed to get download URL: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read download URL response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to get download URL %d: %s", resp.StatusCode, string(body))
	}

	var downloadLink models.DownloadLink
	if err := json.Unmarshal(body, &downloadLink); err != nil {
		return fmt.Errorf("failed to parse download link: %w", err)
	}

	// 2. Создание локальной директории
	dir := filepath.Dir(localPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create local directory: %w", err)
	}

	// 3. Скачивание файла
	req, err := http.NewRequestWithContext(ctx, "GET", downloadLink.Href, nil)
	if err != nil {
		return fmt.Errorf("failed to create download request: %w", err)
	}

	req.Header.Set("Authorization", "OAuth "+c.token)

	downloadResp, err := c.transfer.Do(req)
	if err != nil {
		return fmt.Errorf("download request failed: %w", err)
	}
	defer downloadResp.Body.Close()

	if downloadResp.StatusCode != http.StatusOK {
		downloadBody, _ := io.ReadAll(downloadResp.Body)
		return fmt.Errorf("download failed %d: %s", downloadResp.StatusCode, string(downloadBody))
	}

	// 4. Сохранение в локальный файл (через временный — чтобы обрыв не оставил битый файл)
	tmpPath := localPath + ".yadisk-part"
	file, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}

	_, err = io.Copy(file, downloadResp.Body)
	closeErr := file.Close()
	if err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to save downloaded file: %w", err)
	}
	if closeErr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("failed to close downloaded file: %w", closeErr)
	}

	if err := os.Rename(tmpPath, localPath); err != nil {
		return fmt.Errorf("failed to rename downloaded file: %w", err)
	}

	return nil
}

func (c *Client) DeleteResource(ctx context.Context, remotePath string, permanently bool) error {
	params := map[string]string{
		"path": remotePath,
	}

	if permanently {
		params["permanently"] = "true"
	}

	resp, err := c.doRequest(ctx, "DELETE", "/resources", params, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("failed to delete resource %s: %d %s",
			remotePath, resp.StatusCode, string(body))
	}

	return nil
}

func (c *Client) EnsureFolderExists(ctx context.Context, remotePath string) error {
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
		resource, err := c.GetResource(ctx, fullPath)
		if err != nil {
			return fmt.Errorf("failed to check folder %s: %w", fullPath, err)
		}

		if resource == nil {
			if err := c.CreateFolder(ctx, fullPath); err != nil {
				return fmt.Errorf("failed to create folder %s: %w", fullPath, err)
			}
		} else if resource.Type != "dir" {
			return fmt.Errorf("resource exists but is not a folder: %s", fullPath)
		}
	}

	return nil
}

func (c *Client) GetResourceMetadata(ctx context.Context, remotePath string, fields []string) (*models.Resource, error) {
	params := map[string]string{
		"path":   remotePath,
		"fields": strings.Join(fields, ","),
	}

	resp, err := c.doRequest(ctx, "GET", "/resources", params, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

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

	var resource models.Resource
	if err := json.Unmarshal(body, &resource); err != nil {
		return nil, fmt.Errorf("failed to parse resource metadata: %w", err)
	}

	return &resource, nil
}

func (c *Client) CheckOperationStatus(ctx context.Context, operationURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", operationURL, nil)
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

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read status response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status check failed %d: %s", resp.StatusCode, string(body))
	}

	var operation models.Operation
	if err := json.Unmarshal(body, &operation); err != nil {
		return "", fmt.Errorf("failed to parse operation status: %w", err)
	}

	return operation.Status, nil
}
