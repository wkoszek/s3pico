# s3pico

A minimal, zero-dependency S3-compatible storage server written in Go. Designed for local development, CI pipelines, and unit/integration testing where you need an S3 API without the overhead of a full S3 mock.

## Features

- **Single binary** — runs as both server and CLI client
- **Zero external dependencies** — uses only the Go standard library
- **S3-compatible API** — works with the AWS SDK and `aws` CLI out of the box
- **Multipart upload support** — handles large file uploads via the S3 multipart protocol
- **UUID-based namespacing** — each project gets its own isolated path (acts as a bucket)
- **Embeddable in Go tests** — import `pkg/s3pico` and spin up a server in-process
- **Cross-compile ready** — includes Makefile targets for Linux/amd64 musl static binaries

## Supported S3 Operations

| HTTP Method | Operation | Notes |
|-------------|-----------|-------|
| `PUT /<uuid>` | Create bucket | Creates isolated namespace |
| `PUT /<uuid>/<key>` | Upload object | Single-part upload |
| `GET /<uuid>/<key>` | Download object | |
| `HEAD /<uuid>/<key>` | Object metadata | Returns size and last-modified |
| `DELETE /<uuid>/<key>` | Delete object | |
| `GET /<uuid>/?list-type=2` | List objects | S3 ListObjectsV2 |
| `GET /<uuid>/?prefix=&delimiter=` | List with prefix/delimiter | Common prefix support |
| `POST /<uuid>/<key>?uploads` | Initiate multipart upload | |
| `PUT /<uuid>/<key>?partNumber=N&uploadId=X` | Upload part | |
| `POST /<uuid>/<key>?uploadId=X` | Complete multipart upload | |
| `DELETE /<uuid>/<key>?uploadId=X` | Abort multipart upload | |

## Installation

```bash
go install github.com/wkoszek/s3pico/cmd/s3pico@latest
```

Or build from source:

```bash
git clone https://github.com/wkoszek/s3pico
cd s3pico
make build
```

## Running the Server

```bash
# Default: port 8080, current directory as storage
s3pico server

# Custom port and data directory
s3pico server -port 9000 -data /var/s3pico

# Enable debug logging
s3pico server -debug
```

| Flag | Default | Description |
|------|---------|-------------|
| `-port` | `8080` | TCP port to listen on |
| `-data` | `.` | Directory where files are stored |
| `-debug` | `false` | Log every request and response |

## Client Commands

All client commands accept `-host` (default: `localhost`) and `-port` (default: `8080`) flags.

### Create a bucket

```bash
s3pico mb <uuid>
```

### Upload a file

```bash
s3pico put <local-file> <uuid>/<remote-path>
```

### Download a file

```bash
s3pico get <uuid>/<remote-path>
s3pico get -o output.txt <uuid>/<remote-path>
```

### List files

```bash
s3pico ls <uuid>
```

### Sync a directory

```bash
# Upload local directory to server
s3pico sync <local-dir> <uuid>

# Download from server to local directory
s3pico sync -down <local-dir> <uuid>
```

## Using with AWS CLI

s3pico speaks enough of the S3 protocol to work with the `aws` CLI:

```bash
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1

aws --endpoint-url http://localhost:8080 s3 mb s3://my-bucket-uuid
aws --endpoint-url http://localhost:8080 s3 cp myfile.txt s3://my-bucket-uuid/myfile.txt
aws --endpoint-url http://localhost:8080 s3 ls s3://my-bucket-uuid/
```

## Embedding in Go Tests

Import `pkg/s3pico` to spin up an in-process server for integration tests — no external process required.

```go
import (
    "net/http/httptest"
    "testing"
    "github.com/wkoszek/s3pico/pkg/s3pico"
)

func TestMyFeature(t *testing.T) {
    // Start an in-process S3 server
    server, err := s3pico.NewServer(s3pico.ServerConfig{
        DataDir: t.TempDir(),
    })
    if err != nil {
        t.Fatal(err)
    }

    ts := httptest.NewServer(server.Handler())
    defer ts.Close()

    // Create a client pointing at the test server
    client := s3pico.NewClient(s3pico.ClientConfig{})
    client.SetBaseURL(ts.URL)

    bucketID := "test-bucket-001"
    if err := client.MakeBucket(bucketID); err != nil {
        t.Fatal(err)
    }

    // Upload and retrieve a file
    if err := client.PutBytes([]byte("hello world"), bucketID+"/hello.txt"); err != nil {
        t.Fatal(err)
    }

    data, err := client.Get(bucketID + "/hello.txt")
    if err != nil {
        t.Fatal(err)
    }
    t.Logf("got: %s", data)
}
```

### Client API Reference

| Method | Signature | Description |
|--------|-----------|-------------|
| `MakeBucket` | `(uuid string) error` | Create a new bucket |
| `Put` | `(localPath, remotePath string) error` | Upload file from disk |
| `PutReader` | `(r io.Reader, remotePath string) error` | Upload from any reader |
| `PutBytes` | `(data []byte, remotePath string) error` | Upload from byte slice |
| `Get` | `(remotePath string) ([]byte, error)` | Download to memory |
| `GetToFile` | `(remotePath, localPath string) error` | Download to disk |
| `Delete` | `(remotePath string) error` | Delete an object |
| `Head` | `(remotePath string) (*FileInfo, error)` | Get object metadata |
| `List` | `(bucketPath string) ([]FileInfo, error)` | List objects in bucket |
| `Sync` | `(localDir, uuid string, download bool) error` | Sync directory up or down |
| `SetBaseURL` | `(url string)` | Override server URL (for httptest) |

## Building

```bash
# Build for current platform
make build

# Run tests
make test

# Cross-compile static Linux/amd64 binary (requires musl cross-compiler)
make linux

# Deploy to remote server (configured as "ovh" in SSH config)
make ddd
```

## Path and Security Model

s3pico does not implement AWS authentication. Access control is based on path secrecy: each project uses a UUID as its bucket identifier. Keep UUIDs private or run s3pico behind a firewall/proxy.

All file paths are validated to prevent directory traversal attacks — requests cannot escape the configured data directory.

## License

MIT License

## Author

Adam Koszek <adam@koszek.com>
