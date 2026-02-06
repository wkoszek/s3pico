package s3pico

import (
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const DefaultPort = "8080"
const DefaultHost = "localhost"

// xmlEscape escapes special XML characters in a string
func xmlEscape(s string) string {
	var b strings.Builder
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

type FileInfo struct {
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

type ServerConfig struct {
	DataDir string
	Port    string
	Debug   bool
}

type ClientConfig struct {
	Host string
	Port string
}

type Server struct {
	config     ServerConfig
	httpServer *http.Server
}

type Client struct {
	config          ClientConfig
	client          *http.Client
	baseURLOverride string
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

func NewServer(config ServerConfig) (*Server, error) {
	if config.Port == "" {
		config.Port = DefaultPort
	}
	if config.DataDir == "" {
		config.DataDir = "."
	}

	absDataDir, err := filepath.Abs(config.DataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path: %w", err)
	}
	config.DataDir = absDataDir

	if err := os.MkdirAll(absDataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	return &Server{config: config}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRequest)
	return mux
}

func (s *Server) Start() error {
	s.httpServer = &http.Server{
		Addr:    ":" + s.config.Port,
		Handler: s.Handler(),
	}
	return s.httpServer.ListenAndServe()
}

func (s *Server) Shutdown() error {
	if s.httpServer != nil {
		return s.httpServer.Close()
	}
	return nil
}

func (s *Server) handleRequest(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	clientIP := r.RemoteAddr
	if xForwardedFor := r.Header.Get("X-Forwarded-For"); xForwardedFor != "" {
		clientIP = strings.Split(xForwardedFor, ",")[0]
	}

	if s.config.Debug {
		fmt.Printf("[DEBUG] %s %s %s from %s (Headers: %v, Query: %v)\n",
			time.Now().Format("15:04:05.000"),
			r.Method,
			r.URL.Path,
			clientIP,
			r.Header,
			r.URL.Query())
	}

	wrappedWriter := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		http.Error(wrappedWriter, "Path required", http.StatusBadRequest)
		return
	}

	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 1 || len(parts[0]) < 8 {
		http.Error(wrappedWriter, "Invalid path format (UUID required)", http.StatusBadRequest)
		return
	}

	uuid := parts[0]
	subPath := ""
	if len(parts) > 1 {
		subPath = parts[1]
	}

	// Clean the subPath to resolve any .. components
	cleanSubPath := filepath.Clean(subPath)
	// Reject if it tries to escape the bucket root
	if cleanSubPath == ".." || strings.HasPrefix(cleanSubPath, ".."+string(filepath.Separator)) {
		http.Error(wrappedWriter, "Invalid path", http.StatusBadRequest)
		return
	}

	projectDir := filepath.Join(s.config.DataDir, uuid)
	fullPath := filepath.Join(projectDir, cleanSubPath)

	// Extra safety: verify the resolved path is within projectDir
	// Clean both paths and use trailing separator to prevent prefix attacks (bucket vs bucket2)
	cleanProjectDir := filepath.Clean(projectDir)
	cleanFullPath := filepath.Clean(fullPath)
	if cleanFullPath != cleanProjectDir && !strings.HasPrefix(cleanFullPath, cleanProjectDir+string(filepath.Separator)) {
		http.Error(wrappedWriter, "Invalid path", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case http.MethodGet:
		s.handleGet(wrappedWriter, r, fullPath, projectDir)
	case http.MethodPut:
		if subPath == "" {
			s.handleCreateBucket(wrappedWriter, r, projectDir, uuid)
		} else {
			s.handlePut(wrappedWriter, r, fullPath)
		}
	case http.MethodDelete:
		s.handleDelete(wrappedWriter, r, fullPath)
	case http.MethodHead:
		s.handleHead(wrappedWriter, r, fullPath)
	default:
		http.Error(wrappedWriter, "Method not allowed", http.StatusMethodNotAllowed)
	}

	if s.config.Debug {
		duration := time.Since(start)
		fmt.Printf("[DEBUG] %s %s %s - Status: %d, Duration: %v\n",
			r.Method,
			r.URL.Path,
			clientIP,
			wrappedWriter.statusCode,
			duration)
	}
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request, fullPath, projectDir string) {
	if r.URL.Query().Get("list-type") == "2" || r.URL.Query().Get("delimiter") != "" || r.URL.Query().Get("prefix") != "" {
		s.handleS3List(w, r, projectDir)
		return
	}

	info, err := os.Stat(fullPath)
	if os.IsNotExist(err) {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if info.IsDir() {
		s.handleS3List(w, r, projectDir)
		return
	}

	file, err := os.Open(fullPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	io.Copy(w, file)
}

func (s *Server) handleS3List(w http.ResponseWriter, r *http.Request, projectDir string) {
	prefix := r.URL.Query().Get("prefix")
	delimiter := r.URL.Query().Get("delimiter")

	var contents []map[string]interface{}
	var commonPrefixes []map[string]string
	prefixSet := make(map[string]bool)

	filepath.Walk(projectDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		relPath, _ := filepath.Rel(projectDir, path)
		if relPath == "." {
			return nil
		}

		relPath = strings.ReplaceAll(relPath, string(filepath.Separator), "/")

		if prefix != "" && !strings.HasPrefix(relPath, prefix) {
			return nil
		}

		if delimiter != "" {
			afterPrefix := strings.TrimPrefix(relPath, prefix)
			if idx := strings.Index(afterPrefix, delimiter); idx >= 0 {
				commonPrefix := prefix + afterPrefix[:idx+1]
				if !prefixSet[commonPrefix] {
					prefixSet[commonPrefix] = true
					commonPrefixes = append(commonPrefixes, map[string]string{
						"Prefix": commonPrefix,
					})
				}
				return nil
			}
		}

		if !info.IsDir() {
			contents = append(contents, map[string]interface{}{
				"Key":          relPath,
				"LastModified": info.ModTime().Format(time.RFC3339),
				"Size":         info.Size(),
				"StorageClass": "STANDARD",
			})
		}

		return nil
	})

	w.Header().Set("Content-Type", "application/xml")
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>`))
	w.Write([]byte(`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`))
	w.Write([]byte(fmt.Sprintf(`<Name>%s</Name>`, xmlEscape(strings.Trim(r.URL.Path, "/")))))
	w.Write([]byte(fmt.Sprintf(`<Prefix>%s</Prefix>`, xmlEscape(prefix))))
	w.Write([]byte(fmt.Sprintf(`<MaxKeys>%d</MaxKeys>`, 1000)))
	w.Write([]byte(fmt.Sprintf(`<IsTruncated>%v</IsTruncated>`, false)))

	for _, item := range contents {
		w.Write([]byte(`<Contents>`))
		w.Write([]byte(fmt.Sprintf(`<Key>%s</Key>`, xmlEscape(item["Key"].(string)))))
		w.Write([]byte(fmt.Sprintf(`<LastModified>%s</LastModified>`, item["LastModified"])))
		w.Write([]byte(fmt.Sprintf(`<Size>%d</Size>`, item["Size"])))
		w.Write([]byte(fmt.Sprintf(`<StorageClass>%s</StorageClass>`, item["StorageClass"])))
		w.Write([]byte(`</Contents>`))
	}

	for _, pfx := range commonPrefixes {
		w.Write([]byte(`<CommonPrefixes>`))
		w.Write([]byte(fmt.Sprintf(`<Prefix>%s</Prefix>`, xmlEscape(pfx["Prefix"]))))
		w.Write([]byte(`</CommonPrefixes>`))
	}

	w.Write([]byte(`</ListBucketResult>`))
}

func (s *Server) handleCreateBucket(w http.ResponseWriter, r *http.Request, projectDir, uuid string) {
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Location", "/"+uuid)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handlePut(w http.ResponseWriter, r *http.Request, fullPath string) {
	dir := filepath.Dir(fullPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	file, err := os.Create(fullPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer file.Close()

	_, err = io.Copy(file, r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	info, _ := file.Stat()
	w.Header().Set("ETag", fmt.Sprintf(`"%d"`, info.Size()))
	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request, fullPath string) {
	err := os.Remove(fullPath)
	if os.IsNotExist(err) {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHead(w http.ResponseWriter, r *http.Request, fullPath string) {
	info, err := os.Stat(fullPath)
	if os.IsNotExist(err) {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	w.Header().Set("Last-Modified", info.ModTime().UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusOK)
}

func NewClient(config ClientConfig) *Client {
	if config.Host == "" {
		config.Host = DefaultHost
	}
	if config.Port == "" {
		config.Port = DefaultPort
	}
	return &Client{
		config: config,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// SetBaseURL overrides the computed base URL (useful with httptest.NewServer).
func (c *Client) SetBaseURL(u string) {
	c.baseURLOverride = strings.TrimRight(u, "/")
}

func (c *Client) baseURL() string {
	if c.baseURLOverride != "" {
		return c.baseURLOverride
	}
	return fmt.Sprintf("http://%s:%s", c.config.Host, c.config.Port)
}

// escapePathSegments escapes each segment of a path for safe URL inclusion
func escapePathSegments(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func (c *Client) MakeBucket(uuid string) error {
	reqURL := fmt.Sprintf("%s/%s", c.baseURL(), escapePathSegments(uuid))
	req, err := http.NewRequest(http.MethodPut, reqURL, nil)
	if err != nil {
		return err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to create bucket: %s", body)
	}

	return nil
}

func (c *Client) Put(localPath, remotePath string) error {
	file, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer file.Close()

	return c.PutReader(file, remotePath)
}

func (c *Client) PutReader(r io.Reader, remotePath string) error {
	reqURL := fmt.Sprintf("%s/%s", c.baseURL(), escapePathSegments(remotePath))
	req, err := http.NewRequest(http.MethodPut, reqURL, r)
	if err != nil {
		return err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to upload: %s", body)
	}

	return nil
}

func (c *Client) PutBytes(data []byte, remotePath string) error {
	return c.PutReader(strings.NewReader(string(data)), remotePath)
}

func (c *Client) Get(remotePath string) ([]byte, error) {
	reqURL := fmt.Sprintf("%s/%s", c.baseURL(), escapePathSegments(remotePath))
	resp, err := c.client.Get(reqURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get: %s", body)
	}

	return io.ReadAll(resp.Body)
}

func (c *Client) GetToFile(remotePath, localPath string) error {
	data, err := c.Get(remotePath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return err
	}

	return os.WriteFile(localPath, data, 0644)
}

func (c *Client) Delete(remotePath string) error {
	reqURL := fmt.Sprintf("%s/%s", c.baseURL(), escapePathSegments(remotePath))
	req, err := http.NewRequest(http.MethodDelete, reqURL, nil)
	if err != nil {
		return err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete: %s", body)
	}

	return nil
}

func (c *Client) Head(remotePath string) (*FileInfo, error) {
	reqURL := fmt.Sprintf("%s/%s", c.baseURL(), escapePathSegments(remotePath))
	req, err := http.NewRequest(http.MethodHead, reqURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("not found")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("head failed: status %d", resp.StatusCode)
	}

	var size int64
	fmt.Sscanf(resp.Header.Get("Content-Length"), "%d", &size)

	modTime, _ := time.Parse(http.TimeFormat, resp.Header.Get("Last-Modified"))

	return &FileInfo{
		Size:    size,
		ModTime: modTime,
	}, nil
}

type s3ListResult struct {
	XMLName  xml.Name       `xml:"ListBucketResult"`
	Contents []s3ListObject `xml:"Contents"`
}

type s3ListObject struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	Size         int64  `xml:"Size"`
}

func (c *Client) List(bucketPath string) ([]FileInfo, error) {
	if !strings.HasSuffix(bucketPath, "/") {
		bucketPath += "/"
	}

	reqURL := fmt.Sprintf("%s/%s?list-type=2", c.baseURL(), escapePathSegments(bucketPath))
	resp, err := c.client.Get(reqURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to list: %s", body)
	}

	var result s3ListResult
	if err := xml.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	files := make([]FileInfo, 0, len(result.Contents))
	for _, obj := range result.Contents {
		modTime, _ := time.Parse(time.RFC3339, obj.LastModified)
		files = append(files, FileInfo{
			Name:    obj.Key,
			Size:    obj.Size,
			ModTime: modTime,
		})
	}

	return files, nil
}

func (c *Client) Sync(localDir, uuid string, download bool) error {
	if download {
		return c.syncDown(localDir, uuid)
	}
	return c.syncUp(localDir, uuid)
}

func (c *Client) syncUp(localDir, uuid string) error {
	return filepath.Walk(localDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(localDir, path)
		if err != nil {
			return err
		}

		remotePath := fmt.Sprintf("%s/%s", uuid, relPath)
		return c.Put(path, remotePath)
	})
}

func (c *Client) syncDown(localDir, uuid string) error {
	files, err := c.List(uuid)
	if err != nil {
		return err
	}

	for _, file := range files {
		localPath := filepath.Join(localDir, file.Name)
		localInfo, err := os.Stat(localPath)

		if err == nil && localInfo.Size() == file.Size && !localInfo.ModTime().Before(file.ModTime) {
			continue
		}

		remotePath := fmt.Sprintf("%s/%s", uuid, file.Name)
		if err := c.GetToFile(remotePath, localPath); err != nil {
			return err
		}
	}

	return nil
}
