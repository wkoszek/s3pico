package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/wkoszek/s3pico/pkg/s3pico"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s server|ls|get|put|sync|mb [options]\n", os.Args[0])
		os.Exit(1)
	}

	command := os.Args[1]
	os.Args = append([]string{os.Args[0]}, os.Args[2:]...)

	switch command {
	case "server":
		runServer()
	case "ls":
		runList()
	case "get":
		runGet()
	case "put":
		runPut()
	case "sync":
		runSync()
	case "mb":
		runMakeBucket()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		os.Exit(1)
	}
}

func runServer() {
	dataDir := flag.String("data", ".", "Data directory for storing files")
	port := flag.String("port", s3pico.DefaultPort, "Port to listen on")
	debug := flag.Bool("debug", false, "Enable debug logging")
	flag.Parse()

	server, err := s3pico.NewServer(s3pico.ServerConfig{
		DataDir: *dataDir,
		Port:    *port,
		Debug:   *debug,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create server: %v\n", err)
		os.Exit(1)
	}

	absDataDir, _ := filepath.Abs(*dataDir)
	fmt.Printf("Starting S3 pico server on port %s with data directory: %s\n", *port, absDataDir)
	if err := server.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start server: %v\n", err)
		os.Exit(1)
	}
}

func runList() {
	host := flag.String("host", s3pico.DefaultHost, "S3 pico server host")
	port := flag.String("port", s3pico.DefaultPort, "S3 pico server port")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: s3pico ls [options] <uuid>[/path]\n")
		os.Exit(1)
	}

	client := s3pico.NewClient(s3pico.ClientConfig{Host: *host, Port: *port})
	files, err := client.List(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to list: %v\n", err)
		os.Exit(1)
	}

	for _, file := range files {
		fmt.Printf("%s\t%d\t%s\n", file.Name, file.Size, file.ModTime.Format(time.RFC3339))
	}
}

func runGet() {
	host := flag.String("host", s3pico.DefaultHost, "S3 pico server host")
	port := flag.String("port", s3pico.DefaultPort, "S3 pico server port")
	output := flag.String("o", "", "Output file (default: stdout)")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: s3pico get [options] <uuid>/<path>\n")
		os.Exit(1)
	}

	client := s3pico.NewClient(s3pico.ClientConfig{Host: *host, Port: *port})
	data, err := client.Get(flag.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get file: %v\n", err)
		os.Exit(1)
	}

	var out io.Writer = os.Stdout
	if *output != "" {
		file, err := os.Create(*output)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create output file: %v\n", err)
			os.Exit(1)
		}
		defer file.Close()
		out = file
	}

	out.Write(data)
}

func runMakeBucket() {
	host := flag.String("host", s3pico.DefaultHost, "S3 pico server host")
	port := flag.String("port", s3pico.DefaultPort, "S3 pico server port")
	flag.Parse()

	if flag.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "Usage: s3pico mb [options] <uuid>\n")
		os.Exit(1)
	}

	client := s3pico.NewClient(s3pico.ClientConfig{Host: *host, Port: *port})
	if err := client.MakeBucket(flag.Arg(0)); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create bucket: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Bucket '%s' created successfully\n", flag.Arg(0))
}

func runPut() {
	host := flag.String("host", s3pico.DefaultHost, "S3 pico server host")
	port := flag.String("port", s3pico.DefaultPort, "S3 pico server port")
	flag.Parse()

	if flag.NArg() < 2 {
		fmt.Fprintf(os.Stderr, "Usage: s3pico put [options] <local-file> <uuid>/<path>\n")
		os.Exit(1)
	}

	client := s3pico.NewClient(s3pico.ClientConfig{Host: *host, Port: *port})
	if err := client.Put(flag.Arg(0), flag.Arg(1)); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to upload file: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Upload successful")
}

func runSync() {
	host := flag.String("host", s3pico.DefaultHost, "S3 pico server host")
	port := flag.String("port", s3pico.DefaultPort, "S3 pico server port")
	download := flag.Bool("down", false, "Download from server (default: upload to server)")
	flag.Parse()

	if flag.NArg() < 2 {
		fmt.Fprintf(os.Stderr, "Usage: s3pico sync [options] <local-dir> <uuid>\n")
		os.Exit(1)
	}

	client := s3pico.NewClient(s3pico.ClientConfig{Host: *host, Port: *port})
	if err := client.Sync(flag.Arg(0), flag.Arg(1), *download); err != nil {
		fmt.Fprintf(os.Stderr, "Sync failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Sync successful")
}
