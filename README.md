# s3pico

Minimal S3-compatible storage server in Go.

## Usage

```bash
# Run server
s3pico server -data /path/to/storage -port 8080

# Client commands
s3pico mb <bucket>
s3pico put <local-file> <bucket>/<path>
s3pico get <bucket>/<path>
s3pico ls <bucket>
s3pico sync <local-dir> <bucket>
```

## Build

```bash
make build
make test
```

## License

MIT License

## Author

Adam Koszek <adam@koszek.com>
