package e2e

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"s3pico/pkg/s3pico"
)

func newTestServer(t *testing.T) (*s3pico.Server, string, func()) {
	t.Helper()
	dataDir, err := os.MkdirTemp("", "s3pico-test-*")
	if err != nil {
		t.Fatal(err)
	}

	server, err := s3pico.NewServer(s3pico.ServerConfig{
		DataDir: dataDir,
		Port:    "0",
		Debug:   false,
	})
	if err != nil {
		os.RemoveAll(dataDir)
		t.Fatal(err)
	}

	cleanup := func() {
		server.Shutdown()
		os.RemoveAll(dataDir)
	}

	return server, dataDir, cleanup
}

func newTestServerWithHTTP(t *testing.T) (*httptest.Server, string, func()) {
	t.Helper()
	dataDir, err := os.MkdirTemp("", "s3pico-test-*")
	if err != nil {
		t.Fatal(err)
	}

	server, err := s3pico.NewServer(s3pico.ServerConfig{
		DataDir: dataDir,
		Debug:   false,
	})
	if err != nil {
		os.RemoveAll(dataDir)
		t.Fatal(err)
	}

	ts := httptest.NewServer(server.Handler())

	cleanup := func() {
		ts.Close()
		os.RemoveAll(dataDir)
	}

	return ts, dataDir, cleanup
}

func clientFromTestServer(ts *httptest.Server) *s3pico.Client {
	addr := strings.TrimPrefix(ts.URL, "http://")
	parts := strings.Split(addr, ":")
	return s3pico.NewClient(s3pico.ClientConfig{
		Host: parts[0],
		Port: parts[1],
	})
}

func TestServerCreation(t *testing.T) {
	t.Parallel()

	t.Run("creates data directory", func(t *testing.T) {
		t.Parallel()
		tmpDir, err := os.MkdirTemp("", "s3pico-test-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		dataDir := filepath.Join(tmpDir, "nested", "data", "dir")
		server, err := s3pico.NewServer(s3pico.ServerConfig{
			DataDir: dataDir,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer server.Shutdown()

		if _, err := os.Stat(dataDir); os.IsNotExist(err) {
			t.Error("data directory was not created")
		}
	})

	t.Run("uses default port", func(t *testing.T) {
		t.Parallel()
		tmpDir, err := os.MkdirTemp("", "s3pico-test-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		_, err = s3pico.NewServer(s3pico.ServerConfig{
			DataDir: tmpDir,
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestMakeBucket(t *testing.T) {
	t.Parallel()
	ts, dataDir, cleanup := newTestServerWithHTTP(t)
	defer cleanup()

	client := clientFromTestServer(ts)

	t.Run("creates bucket with valid UUID", func(t *testing.T) {
		uuid := "12345678-test-bucket"

		// 1. Create bucket
		err := client.MakeBucket(uuid)
		if err != nil {
			t.Fatalf("MakeBucket failed: %v", err)
		}

		// 2. Verify via filesystem
		bucketPath := filepath.Join(dataDir, uuid)
		if _, err := os.Stat(bucketPath); os.IsNotExist(err) {
			t.Error("bucket directory was not created")
		}

		// 3. Verify via List API - should be empty
		files, err := client.List(uuid)
		if err != nil {
			t.Fatalf("List on new bucket failed: %v", err)
		}
		if len(files) != 0 {
			t.Errorf("new bucket should be empty, got %d files", len(files))
		}

		// 4. Verify can Put to bucket
		testContent := []byte("bucket works")
		err = client.PutBytes(testContent, uuid+"/verify.txt")
		if err != nil {
			t.Errorf("Put to new bucket failed: %v", err)
		}

		// 5. Verify file appears in List
		files, err = client.List(uuid)
		if err != nil {
			t.Fatalf("List after put failed: %v", err)
		}
		if len(files) != 1 {
			t.Errorf("bucket should have 1 file, got %d", len(files))
		}
	})

	t.Run("fails with short UUID", func(t *testing.T) {
		uuid := "short"
		err := client.MakeBucket(uuid)
		if err == nil {
			t.Error("expected error for short UUID")
		}
	})
}

func TestPutAndGet(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newTestServerWithHTTP(t)
	defer cleanup()

	client := clientFromTestServer(ts)

	bucket := "testbucket1234"
	if err := client.MakeBucket(bucket); err != nil {
		t.Fatalf("MakeBucket failed: %v", err)
	}

	t.Run("put and get small file", func(t *testing.T) {
		content := []byte("hello world")
		remotePath := bucket + "/small.txt"

		// 1. Put the file
		err := client.PutBytes(content, remotePath)
		if err != nil {
			t.Fatalf("PutBytes failed: %v", err)
		}

		// 2. Verify via Get - content matches
		got, err := client.Get(remotePath)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if !bytes.Equal(got, content) {
			t.Errorf("content mismatch: got %q, want %q", got, content)
		}

		// 3. Verify via Head - size matches
		info, err := client.Head(remotePath)
		if err != nil {
			t.Fatalf("Head failed: %v", err)
		}
		if info.Size != int64(len(content)) {
			t.Errorf("Head size mismatch: got %d, want %d", info.Size, len(content))
		}

		// 4. Verify via List - file appears
		files, err := client.List(bucket)
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}
		found := false
		for _, f := range files {
			if f.Name == "small.txt" {
				found = true
				if f.Size != int64(len(content)) {
					t.Errorf("List size mismatch: got %d, want %d", f.Size, len(content))
				}
				break
			}
		}
		if !found {
			t.Error("file not found in List")
		}
	})

	t.Run("put and get binary data", func(t *testing.T) {
		content := make([]byte, 1024)
		rand.Read(content)
		remotePath := bucket + "/binary.dat"

		// 1. Put
		err := client.PutBytes(content, remotePath)
		if err != nil {
			t.Fatalf("PutBytes failed: %v", err)
		}

		// 2. Verify via Get
		got, err := client.Get(remotePath)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if !bytes.Equal(got, content) {
			t.Error("binary content mismatch")
		}

		// 3. Verify via Head
		info, err := client.Head(remotePath)
		if err != nil {
			t.Fatalf("Head failed: %v", err)
		}
		if info.Size != int64(len(content)) {
			t.Errorf("Head size mismatch: got %d, want %d", info.Size, len(content))
		}
	})

	t.Run("put and get large file", func(t *testing.T) {
		content := make([]byte, 1024*1024) // 1MB
		rand.Read(content)
		remotePath := bucket + "/large.bin"

		// 1. Put
		err := client.PutBytes(content, remotePath)
		if err != nil {
			t.Fatalf("PutBytes failed: %v", err)
		}

		// 2. Verify via Get
		got, err := client.Get(remotePath)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if !bytes.Equal(got, content) {
			t.Error("large file content mismatch")
		}

		// 3. Verify via Head - especially important for large files
		info, err := client.Head(remotePath)
		if err != nil {
			t.Fatalf("Head failed: %v", err)
		}
		if info.Size != int64(len(content)) {
			t.Errorf("Head size mismatch: got %d, want %d", info.Size, len(content))
		}
	})

	t.Run("put creates nested directories", func(t *testing.T) {
		content := []byte("nested content")
		remotePath := bucket + "/a/b/c/d/nested.txt"

		// 1. Put
		err := client.PutBytes(content, remotePath)
		if err != nil {
			t.Fatalf("PutBytes failed: %v", err)
		}

		// 2. Verify via Get
		got, err := client.Get(remotePath)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if !bytes.Equal(got, content) {
			t.Errorf("content mismatch: got %q, want %q", got, content)
		}

		// 3. Verify via Head
		info, err := client.Head(remotePath)
		if err != nil {
			t.Fatalf("Head failed: %v", err)
		}
		if info.Size != int64(len(content)) {
			t.Errorf("Head size mismatch: got %d, want %d", info.Size, len(content))
		}

		// 4. Verify file appears in List with prefix
		files, err := client.List(bucket)
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}
		found := false
		for _, f := range files {
			if f.Name == "a/b/c/d/nested.txt" {
				found = true
				break
			}
		}
		if !found {
			t.Error("nested file not found in List")
		}
	})

	t.Run("get nonexistent file returns error", func(t *testing.T) {
		_, err := client.Get(bucket + "/nonexistent.txt")
		if err == nil {
			t.Error("expected error for nonexistent file")
		}

		// Also verify Head fails
		_, err = client.Head(bucket + "/nonexistent.txt")
		if err == nil {
			t.Error("expected Head error for nonexistent file")
		}
	})
}

func TestDelete(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newTestServerWithHTTP(t)
	defer cleanup()

	client := clientFromTestServer(ts)

	bucket := "deletebucket1"
	if err := client.MakeBucket(bucket); err != nil {
		t.Fatal(err)
	}

	t.Run("delete existing file", func(t *testing.T) {
		content := []byte("delete me")
		remotePath := bucket + "/to-delete.txt"

		// 1. Create the file
		if err := client.PutBytes(content, remotePath); err != nil {
			t.Fatal(err)
		}

		// 2. Verify file exists via Head before delete
		info, err := client.Head(remotePath)
		if err != nil {
			t.Fatalf("Head before delete failed: %v", err)
		}
		if info.Size != int64(len(content)) {
			t.Errorf("size mismatch before delete: got %d, want %d", info.Size, len(content))
		}

		// 3. Verify file appears in List before delete
		filesBefore, err := client.List(bucket)
		if err != nil {
			t.Fatalf("List before delete failed: %v", err)
		}
		foundBefore := false
		for _, f := range filesBefore {
			if f.Name == "to-delete.txt" {
				foundBefore = true
				break
			}
		}
		if !foundBefore {
			t.Error("file should appear in List before delete")
		}

		// 4. Delete the file
		err = client.Delete(remotePath)
		if err != nil {
			t.Fatalf("Delete failed: %v", err)
		}

		// 5. Verify Get fails after delete
		_, err = client.Get(remotePath)
		if err == nil {
			t.Error("Get should fail after delete")
		}

		// 6. Verify Head fails after delete
		_, err = client.Head(remotePath)
		if err == nil {
			t.Error("Head should fail after delete")
		}

		// 7. Verify file no longer appears in List
		filesAfter, err := client.List(bucket)
		if err != nil {
			t.Fatalf("List after delete failed: %v", err)
		}
		for _, f := range filesAfter {
			if f.Name == "to-delete.txt" {
				t.Error("file should not appear in List after delete")
				break
			}
		}
	})

	t.Run("delete nonexistent file returns error", func(t *testing.T) {
		err := client.Delete(bucket + "/nonexistent.txt")
		if err == nil {
			t.Error("expected error for nonexistent file")
		}
	})
}

func TestHead(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newTestServerWithHTTP(t)
	defer cleanup()

	client := clientFromTestServer(ts)

	bucket := "headbucket12"
	if err := client.MakeBucket(bucket); err != nil {
		t.Fatal(err)
	}

	t.Run("head existing file", func(t *testing.T) {
		content := []byte("check my head")
		remotePath := bucket + "/headtest.txt"

		timeBefore := time.Now().Add(-1 * time.Second)

		// 1. Create file
		if err := client.PutBytes(content, remotePath); err != nil {
			t.Fatal(err)
		}

		timeAfter := time.Now().Add(1 * time.Second)

		// 2. Head the file
		info, err := client.Head(remotePath)
		if err != nil {
			t.Fatalf("Head failed: %v", err)
		}

		// 3. Verify size
		if info.Size != int64(len(content)) {
			t.Errorf("size mismatch: got %d, want %d", info.Size, len(content))
		}

		// 4. Verify ModTime is not zero
		if info.ModTime.IsZero() {
			t.Error("ModTime should not be zero")
		}

		// 5. Verify ModTime is recent (between before and after)
		if info.ModTime.Before(timeBefore) {
			t.Errorf("ModTime %v is before test start %v", info.ModTime, timeBefore)
		}
		if info.ModTime.After(timeAfter) {
			t.Errorf("ModTime %v is after test end %v", info.ModTime, timeAfter)
		}

		// 6. Verify Get returns same size
		data, err := client.Get(remotePath)
		if err != nil {
			t.Fatalf("Get failed: %v", err)
		}
		if int64(len(data)) != info.Size {
			t.Errorf("Get size %d doesn't match Head size %d", len(data), info.Size)
		}
	})

	t.Run("head nonexistent file", func(t *testing.T) {
		_, err := client.Head(bucket + "/nonexistent.txt")
		if err == nil {
			t.Error("expected error for nonexistent file")
		}
	})
}

func TestConcurrentOperations(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newTestServerWithHTTP(t)
	defer cleanup()

	client := clientFromTestServer(ts)

	bucket := "concurrentbkt"
	if err := client.MakeBucket(bucket); err != nil {
		t.Fatal(err)
	}

	t.Run("concurrent uploads", func(t *testing.T) {
		numFiles := 50
		var wg sync.WaitGroup
		errors := make(chan error, numFiles)

		// 1. Upload files concurrently
		for i := 0; i < numFiles; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				content := []byte(fmt.Sprintf("content-%d", idx))
				remotePath := fmt.Sprintf("%s/concurrent-%d.txt", bucket, idx)

				if err := client.PutBytes(content, remotePath); err != nil {
					errors <- err
				}
			}(i)
		}

		wg.Wait()
		close(errors)

		for err := range errors {
			t.Errorf("concurrent upload failed: %v", err)
		}

		// 2. Verify all files exist via List
		files, err := client.List(bucket)
		if err != nil {
			t.Fatalf("List after concurrent uploads failed: %v", err)
		}

		// Count concurrent files
		concurrentCount := 0
		for _, f := range files {
			if strings.HasPrefix(f.Name, "concurrent-") {
				concurrentCount++
			}
		}
		if concurrentCount != numFiles {
			t.Errorf("expected %d concurrent files in List, got %d", numFiles, concurrentCount)
		}

		// 3. Verify each file content
		for i := 0; i < numFiles; i++ {
			remotePath := fmt.Sprintf("%s/concurrent-%d.txt", bucket, i)
			expectedContent := fmt.Sprintf("content-%d", i)

			got, err := client.Get(remotePath)
			if err != nil {
				t.Errorf("Get concurrent-%d.txt failed: %v", i, err)
				continue
			}
			if string(got) != expectedContent {
				t.Errorf("content mismatch for concurrent-%d.txt: got %q, want %q", i, got, expectedContent)
			}
		}
	})

	t.Run("concurrent reads", func(t *testing.T) {
		// First upload a file
		remotePath := bucket + "/read-test.txt"
		content := []byte("read me concurrently")
		if err := client.PutBytes(content, remotePath); err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		errors := make(chan error, 100)

		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				got, err := client.Get(remotePath)
				if err != nil {
					errors <- err
					return
				}
				if !bytes.Equal(got, content) {
					errors <- fmt.Errorf("content mismatch")
				}
			}()
		}

		wg.Wait()
		close(errors)

		for err := range errors {
			t.Errorf("concurrent read failed: %v", err)
		}
	})

	t.Run("concurrent mixed operations", func(t *testing.T) {
		var wg sync.WaitGroup
		errors := make(chan error, 100)

		for i := 0; i < 25; i++ {
			wg.Add(4)

			// Upload
			go func(idx int) {
				defer wg.Done()
				remotePath := fmt.Sprintf("%s/mixed-%d.txt", bucket, idx)
				if err := client.PutBytes([]byte("data"), remotePath); err != nil {
					errors <- err
				}
			}(i)

			// Read
			go func() {
				defer wg.Done()
				client.Get(bucket + "/read-test.txt")
			}()

			// Head
			go func() {
				defer wg.Done()
				client.Head(bucket + "/read-test.txt")
			}()

			// Delete (non-critical, file may not exist)
			go func(idx int) {
				defer wg.Done()
				client.Delete(fmt.Sprintf("%s/mixed-%d.txt", bucket, idx+1000))
			}(i)
		}

		wg.Wait()
		close(errors)

		for err := range errors {
			t.Errorf("concurrent mixed operation failed: %v", err)
		}
	})
}

func TestPathTraversal(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newTestServerWithHTTP(t)
	defer cleanup()

	bucket := "securitytest1"

	t.Run("path traversal attack blocked", func(t *testing.T) {
		maliciousPaths := []string{
			bucket + "/../../../etc/passwd",
			bucket + "/..%2F..%2F..%2Fetc%2Fpasswd",
			bucket + "/foo/../../bar",
		}

		for _, path := range maliciousPaths {
			url := ts.URL + "/" + path
			resp, err := http.Get(url)
			if err != nil {
				continue
			}
			resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				t.Errorf("path traversal should be blocked for: %s", path)
			}
		}
	})
}

func TestS3ListAPI(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newTestServerWithHTTP(t)
	defer cleanup()

	client := clientFromTestServer(ts)

	bucket := "listapibucket"
	if err := client.MakeBucket(bucket); err != nil {
		t.Fatal(err)
	}

	// Upload some test files with known content
	testFiles := map[string]string{
		"file1.txt":      "content1",
		"file2.txt":      "content2",
		"dir1/file3.txt": "content3",
		"dir1/file4.txt": "content4",
		"dir2/file5.txt": "content5",
	}
	for f, content := range testFiles {
		if err := client.PutBytes([]byte(content), bucket+"/"+f); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("list with list-type=2", func(t *testing.T) {
		url := fmt.Sprintf("%s/%s?list-type=2", ts.URL, bucket)
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}

		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)

		// 1. Verify XML structure
		if !strings.Contains(bodyStr, "<ListBucketResult") {
			t.Error("response should be XML ListBucketResult")
		}
		if !strings.Contains(bodyStr, "</ListBucketResult>") {
			t.Error("response should have closing ListBucketResult tag")
		}

		// 2. Count <Contents> entries - should be 5
		contentsCount := strings.Count(bodyStr, "<Contents>")
		if contentsCount != len(testFiles) {
			t.Errorf("expected %d <Contents> entries, got %d", len(testFiles), contentsCount)
		}

		// 3. Verify each file appears
		for f := range testFiles {
			if !strings.Contains(bodyStr, "<Key>"+f+"</Key>") {
				t.Errorf("file %s should appear in List", f)
			}
		}

		// 4. Cross-verify with client.List
		clientFiles, err := client.List(bucket)
		if err != nil {
			t.Fatalf("client.List failed: %v", err)
		}
		if len(clientFiles) != len(testFiles) {
			t.Errorf("client.List returned %d files, expected %d", len(clientFiles), len(testFiles))
		}
	})

	t.Run("list with prefix filter", func(t *testing.T) {
		url := fmt.Sprintf("%s/%s?list-type=2&prefix=dir1/", ts.URL, bucket)
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)

		// 1. Should contain dir1 files
		if !strings.Contains(bodyStr, "<Key>dir1/file3.txt</Key>") {
			t.Error("should contain dir1/file3.txt")
		}
		if !strings.Contains(bodyStr, "<Key>dir1/file4.txt</Key>") {
			t.Error("should contain dir1/file4.txt")
		}

		// 2. Should NOT contain files outside dir1
		if strings.Contains(bodyStr, "<Key>file1.txt</Key>") {
			t.Error("should not contain file1.txt (outside prefix)")
		}
		if strings.Contains(bodyStr, "<Key>dir2/file5.txt</Key>") {
			t.Error("should not contain dir2/file5.txt (outside prefix)")
		}

		// 3. Verify count - exactly 2 files in dir1
		contentsCount := strings.Count(bodyStr, "<Contents>")
		if contentsCount != 2 {
			t.Errorf("expected 2 files in dir1/, got %d", contentsCount)
		}
	})

	t.Run("list with delimiter", func(t *testing.T) {
		url := fmt.Sprintf("%s/%s?list-type=2&delimiter=/&prefix=", ts.URL, bucket)
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		bodyStr := string(body)

		// 1. Should contain top-level files
		if !strings.Contains(bodyStr, "<Key>file1.txt</Key>") {
			t.Error("should contain file1.txt")
		}
		if !strings.Contains(bodyStr, "<Key>file2.txt</Key>") {
			t.Error("should contain file2.txt")
		}

		// 2. Verify response is valid XML
		if !strings.Contains(bodyStr, "<ListBucketResult") {
			t.Error("response should be XML ListBucketResult")
		}

		// 3. Verify at least top-level files present
		contentsCount := strings.Count(bodyStr, "<Contents>")
		if contentsCount < 2 {
			t.Errorf("expected at least 2 files, got %d", contentsCount)
		}
	})
}

func TestHTTPMethods(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newTestServerWithHTTP(t)
	defer cleanup()

	client := clientFromTestServer(ts)

	bucket := "methodstest1"
	if err := client.MakeBucket(bucket); err != nil {
		t.Fatal(err)
	}

	t.Run("unsupported method returns 405", func(t *testing.T) {
		url := ts.URL + "/" + bucket + "/file.txt"
		req, _ := http.NewRequest(http.MethodPatch, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("expected 405, got %d", resp.StatusCode)
		}
	})

	t.Run("empty path returns 400", func(t *testing.T) {
		url := ts.URL + "/"
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("expected 400, got %d", resp.StatusCode)
		}
	})
}

func TestClientConfig(t *testing.T) {
	t.Parallel()

	t.Run("uses defaults when not specified", func(t *testing.T) {
		client := s3pico.NewClient(s3pico.ClientConfig{})
		if client == nil {
			t.Error("client should not be nil")
		}
	})

	t.Run("uses custom config", func(t *testing.T) {
		client := s3pico.NewClient(s3pico.ClientConfig{
			Host: "custom.host",
			Port: "9999",
		})
		if client == nil {
			t.Error("client should not be nil")
		}
	})
}

func TestPutFromFile(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newTestServerWithHTTP(t)
	defer cleanup()

	client := clientFromTestServer(ts)

	bucket := "fileuploads1"
	if err := client.MakeBucket(bucket); err != nil {
		t.Fatal(err)
	}

	t.Run("upload from local file", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "upload-test-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(tmpFile.Name())

		content := []byte("file upload test content")
		if _, err := tmpFile.Write(content); err != nil {
			t.Fatal(err)
		}
		tmpFile.Close()

		remotePath := bucket + "/uploaded-file.txt"
		if err := client.Put(tmpFile.Name(), remotePath); err != nil {
			t.Fatalf("Put failed: %v", err)
		}

		got, err := client.Get(remotePath)
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(got, content) {
			t.Errorf("content mismatch: got %q, want %q", got, content)
		}
	})

	t.Run("upload nonexistent file returns error", func(t *testing.T) {
		err := client.Put("/nonexistent/path/file.txt", bucket+"/test.txt")
		if err == nil {
			t.Error("expected error for nonexistent file")
		}
	})
}

func TestGetToFile(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newTestServerWithHTTP(t)
	defer cleanup()

	client := clientFromTestServer(ts)

	bucket := "filedownload1"
	if err := client.MakeBucket(bucket); err != nil {
		t.Fatal(err)
	}

	t.Run("download to local file", func(t *testing.T) {
		content := []byte("download test content")
		remotePath := bucket + "/download-me.txt"
		if err := client.PutBytes(content, remotePath); err != nil {
			t.Fatal(err)
		}

		tmpDir, err := os.MkdirTemp("", "download-test-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		localPath := filepath.Join(tmpDir, "downloaded.txt")
		if err := client.GetToFile(remotePath, localPath); err != nil {
			t.Fatalf("GetToFile failed: %v", err)
		}

		got, err := os.ReadFile(localPath)
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Equal(got, content) {
			t.Errorf("content mismatch: got %q, want %q", got, content)
		}
	})

	t.Run("download creates nested directories", func(t *testing.T) {
		content := []byte("nested download content")
		remotePath := bucket + "/nested-download.txt"
		if err := client.PutBytes(content, remotePath); err != nil {
			t.Fatal(err)
		}

		tmpDir, err := os.MkdirTemp("", "download-nested-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(tmpDir)

		localPath := filepath.Join(tmpDir, "a", "b", "c", "downloaded.txt")
		if err := client.GetToFile(remotePath, localPath); err != nil {
			t.Fatalf("GetToFile failed: %v", err)
		}

		if _, err := os.Stat(localPath); os.IsNotExist(err) {
			t.Error("file should exist")
		}
	})
}

func TestEdgeCases(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newTestServerWithHTTP(t)
	defer cleanup()

	client := clientFromTestServer(ts)

	bucket := "edgecasebkt1"
	if err := client.MakeBucket(bucket); err != nil {
		t.Fatal(err)
	}

	t.Run("empty file", func(t *testing.T) {
		remotePath := bucket + "/empty.txt"

		// 1. Put empty file
		if err := client.PutBytes([]byte{}, remotePath); err != nil {
			t.Fatal(err)
		}

		// 2. Verify via Get - empty content
		got, err := client.Get(remotePath)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 0 {
			t.Errorf("expected empty file, got %d bytes", len(got))
		}

		// 3. Verify via Head - size 0
		info, err := client.Head(remotePath)
		if err != nil {
			t.Fatalf("Head failed: %v", err)
		}
		if info.Size != 0 {
			t.Errorf("Head size should be 0, got %d", info.Size)
		}

		// 4. Verify appears in List with size 0
		files, err := client.List(bucket)
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}
		found := false
		for _, f := range files {
			if f.Name == "empty.txt" {
				found = true
				if f.Size != 0 {
					t.Errorf("List size should be 0, got %d", f.Size)
				}
				break
			}
		}
		if !found {
			t.Error("empty file not found in List")
		}
	})

	t.Run("special characters in filename", func(t *testing.T) {
		specialNames := []string{
			"file with spaces.txt",
			"file-with-dashes.txt",
			"file_with_underscores.txt",
			"file.multiple.dots.txt",
		}

		for _, name := range specialNames {
			remotePath := bucket + "/" + name
			content := []byte("content for " + name)

			// 1. Put
			if err := client.PutBytes(content, remotePath); err != nil {
				t.Errorf("PutBytes failed for %s: %v", name, err)
				continue
			}

			// 2. Verify via Get
			got, err := client.Get(remotePath)
			if err != nil {
				t.Errorf("Get failed for %s: %v", name, err)
				continue
			}
			if !bytes.Equal(got, content) {
				t.Errorf("content mismatch for %s", name)
			}

			// 3. Verify via Head
			info, err := client.Head(remotePath)
			if err != nil {
				t.Errorf("Head failed for %s: %v", name, err)
				continue
			}
			if info.Size != int64(len(content)) {
				t.Errorf("Head size mismatch for %s: got %d, want %d", name, info.Size, len(content))
			}
		}

		// 4. Verify all appear in List
		files, err := client.List(bucket)
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}
		for _, name := range specialNames {
			found := false
			for _, f := range files {
				if f.Name == name {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("special file %q not found in List", name)
			}
		}
	})

	t.Run("overwrite existing file", func(t *testing.T) {
		remotePath := bucket + "/overwrite.txt"
		originalContent := []byte("original")
		updatedContent := []byte("updated")

		// 1. Put original
		if err := client.PutBytes(originalContent, remotePath); err != nil {
			t.Fatal(err)
		}

		// 2. Verify original via Head
		info1, err := client.Head(remotePath)
		if err != nil {
			t.Fatalf("Head after original put failed: %v", err)
		}
		if info1.Size != int64(len(originalContent)) {
			t.Errorf("original size mismatch: got %d, want %d", info1.Size, len(originalContent))
		}

		// 3. Overwrite with updated content
		if err := client.PutBytes(updatedContent, remotePath); err != nil {
			t.Fatal(err)
		}

		// 4. Verify updated via Get
		got, err := client.Get(remotePath)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "updated" {
			t.Errorf("expected 'updated', got %q", got)
		}

		// 5. Verify updated size via Head
		info2, err := client.Head(remotePath)
		if err != nil {
			t.Fatalf("Head after update failed: %v", err)
		}
		if info2.Size != int64(len(updatedContent)) {
			t.Errorf("updated size mismatch: got %d, want %d", info2.Size, len(updatedContent))
		}

		// 6. Verify size changed (original was 8 bytes, updated is 7)
		if info1.Size == info2.Size {
			t.Error("size should have changed after overwrite")
		}

		// 7. Verify only 1 file in List (not duplicated)
		files, err := client.List(bucket)
		if err != nil {
			t.Fatalf("List failed: %v", err)
		}
		count := 0
		for _, f := range files {
			if f.Name == "overwrite.txt" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("expected 1 overwrite.txt in List, found %d", count)
		}
	})
}

func TestResponseHeaders(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newTestServerWithHTTP(t)
	defer cleanup()

	client := clientFromTestServer(ts)

	bucket := "headerstest1"
	if err := client.MakeBucket(bucket); err != nil {
		t.Fatal(err)
	}

	t.Run("GET returns correct headers", func(t *testing.T) {
		content := []byte("header test content")
		remotePath := bucket + "/headers.txt"
		if err := client.PutBytes(content, remotePath); err != nil {
			t.Fatal(err)
		}

		url := ts.URL + "/" + remotePath
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		contentType := resp.Header.Get("Content-Type")
		if contentType != "application/octet-stream" {
			t.Errorf("expected application/octet-stream, got %s", contentType)
		}

		contentLength := resp.Header.Get("Content-Length")
		if contentLength != fmt.Sprintf("%d", len(content)) {
			t.Errorf("expected Content-Length %d, got %s", len(content), contentLength)
		}
	})

	t.Run("PUT returns ETag", func(t *testing.T) {
		url := ts.URL + "/" + bucket + "/etag-test.txt"
		req, _ := http.NewRequest(http.MethodPut, url, strings.NewReader("etag content"))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		etag := resp.Header.Get("ETag")
		if etag == "" {
			t.Error("expected ETag header")
		}
	})

	t.Run("HEAD returns Last-Modified", func(t *testing.T) {
		remotePath := bucket + "/lastmod.txt"
		if err := client.PutBytes([]byte("last mod test"), remotePath); err != nil {
			t.Fatal(err)
		}

		url := ts.URL + "/" + remotePath
		req, _ := http.NewRequest(http.MethodHead, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		lastMod := resp.Header.Get("Last-Modified")
		if lastMod == "" {
			t.Error("expected Last-Modified header")
		}

		_, err = time.Parse(http.TimeFormat, lastMod)
		if err != nil {
			t.Errorf("invalid Last-Modified format: %v", err)
		}
	})

	t.Run("bucket creation returns Location", func(t *testing.T) {
		newBucket := "locationtest1"
		url := ts.URL + "/" + newBucket
		req, _ := http.NewRequest(http.MethodPut, url, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		location := resp.Header.Get("Location")
		if location != "/"+newBucket {
			t.Errorf("expected Location /%s, got %s", newBucket, location)
		}
	})
}

func TestServerDebugMode(t *testing.T) {
	t.Parallel()

	dataDir, err := os.MkdirTemp("", "s3pico-debug-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dataDir)

	server, err := s3pico.NewServer(s3pico.ServerConfig{
		DataDir: dataDir,
		Debug:   true,
	})
	if err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(server.Handler())
	defer ts.Close()

	// Make a request to trigger debug logging
	resp, err := http.Get(ts.URL + "/testbucket123/file.txt")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestServerErrors(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newTestServerWithHTTP(t)
	defer cleanup()

	client := clientFromTestServer(ts)

	t.Run("get from nonexistent bucket", func(t *testing.T) {
		_, err := client.Get("nonexistent123/file.txt")
		if err == nil {
			t.Error("expected error")
		}
	})

	t.Run("delete from nonexistent bucket", func(t *testing.T) {
		err := client.Delete("nonexistent123/file.txt")
		if err == nil {
			t.Error("expected error")
		}
	})

	t.Run("head nonexistent bucket", func(t *testing.T) {
		_, err := client.Head("nonexistent123/file.txt")
		if err == nil {
			t.Error("expected error")
		}
	})

	t.Run("list nonexistent bucket returns empty", func(t *testing.T) {
		files, err := client.List("nonexistent123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(files) != 0 {
			t.Errorf("expected empty list, got %d files", len(files))
		}
	})
}

func TestClientSync(t *testing.T) {
	t.Parallel()
	ts, _, cleanup := newTestServerWithHTTP(t)
	defer cleanup()

	client := clientFromTestServer(ts)

	bucket := "synctestbkt1"
	if err := client.MakeBucket(bucket); err != nil {
		t.Fatal(err)
	}

	t.Run("sync up uploads files", func(t *testing.T) {
		// Create local directory with files
		localDir, err := os.MkdirTemp("", "sync-up-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(localDir)

		// Create test files
		files := map[string]string{
			"file1.txt":        "content 1",
			"file2.txt":        "content 2",
			"subdir/file3.txt": "content 3",
		}
		for name, content := range files {
			path := filepath.Join(localDir, name)
			os.MkdirAll(filepath.Dir(path), 0755)
			os.WriteFile(path, []byte(content), 0644)
		}

		// Sync up
		err = client.Sync(localDir, bucket, false)
		if err != nil {
			t.Fatalf("Sync up failed: %v", err)
		}

		// Verify files were uploaded
		for name, expectedContent := range files {
			remotePath := bucket + "/" + name
			got, err := client.Get(remotePath)
			if err != nil {
				t.Errorf("Get %s failed: %v", name, err)
				continue
			}
			if string(got) != expectedContent {
				t.Errorf("content mismatch for %s: got %q, want %q", name, got, expectedContent)
			}
		}
	})

	t.Run("sync down downloads files", func(t *testing.T) {
		// First upload some files
		uploadBucket := "syncdownbkt1"
		if err := client.MakeBucket(uploadBucket); err != nil {
			t.Fatal(err)
		}

		files := map[string]string{
			"down1.txt": "download content 1",
			"down2.txt": "download content 2",
		}
		for name, content := range files {
			if err := client.PutBytes([]byte(content), uploadBucket+"/"+name); err != nil {
				t.Fatalf("upload failed: %v", err)
			}
		}

		// Create local directory
		localDir, err := os.MkdirTemp("", "sync-down-*")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(localDir)

		// Sync down
		err = client.Sync(localDir, uploadBucket, true)
		if err != nil {
			t.Fatalf("Sync down failed: %v", err)
		}

		// Verify files were downloaded
		for name, expectedContent := range files {
			localPath := filepath.Join(localDir, name)
			got, err := os.ReadFile(localPath)
			if err != nil {
				t.Errorf("Read %s failed: %v", name, err)
				continue
			}
			if string(got) != expectedContent {
				t.Errorf("content mismatch for %s: got %q, want %q", name, got, expectedContent)
			}
		}
	})
}

func BenchmarkPutSmallFile(b *testing.B) {
	ts, _, cleanup := newTestServerWithHTTP(&testing.T{})
	defer cleanup()

	client := clientFromTestServer(ts)
	bucket := "benchbucket1"
	client.MakeBucket(bucket)

	content := []byte("small content for benchmark")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		remotePath := fmt.Sprintf("%s/bench-%d.txt", bucket, i)
		client.PutBytes(content, remotePath)
	}
}

func BenchmarkPutLargeFile(b *testing.B) {
	ts, _, cleanup := newTestServerWithHTTP(&testing.T{})
	defer cleanup()

	client := clientFromTestServer(ts)
	bucket := "benchbucket2"
	client.MakeBucket(bucket)

	content := make([]byte, 1024*1024) // 1MB
	rand.Read(content)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		remotePath := fmt.Sprintf("%s/bench-%d.bin", bucket, i)
		client.PutBytes(content, remotePath)
	}
}

func BenchmarkGetFile(b *testing.B) {
	ts, _, cleanup := newTestServerWithHTTP(&testing.T{})
	defer cleanup()

	client := clientFromTestServer(ts)
	bucket := "benchbucket3"
	client.MakeBucket(bucket)

	remotePath := bucket + "/bench-read.txt"
	client.PutBytes([]byte("benchmark read content"), remotePath)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		client.Get(remotePath)
	}
}

func BenchmarkConcurrentOperations(b *testing.B) {
	ts, _, cleanup := newTestServerWithHTTP(&testing.T{})
	defer cleanup()

	client := clientFromTestServer(ts)
	bucket := "benchbucket4"
	client.MakeBucket(bucket)

	remotePath := bucket + "/bench-concurrent.txt"
	client.PutBytes([]byte("concurrent benchmark content"), remotePath)

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%2 == 0 {
				client.Get(remotePath)
			} else {
				client.PutBytes([]byte("data"), fmt.Sprintf("%s/parallel-%d.txt", bucket, i))
			}
			i++
		}
	})
}
