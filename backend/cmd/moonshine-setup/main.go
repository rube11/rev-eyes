//go:build moonshine && cgo

// Downloads the pinned runtime's English tiny-streaming model with checksum verification.
package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/rube11/rev-eyes/backend/internal/stt/moonshine"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("usage: moonshine-setup MODEL_DIRECTORY")
	}
	raw, err := moonshine.ModelManifest()
	if err != nil {
		return err
	}
	var manifest struct {
		Groups []struct {
			Files []struct {
				Name, URL, Checksum string
				Size                int64
				ChecksumType        string `json:"checksum_type"`
			}
		}
	}
	if err = json.Unmarshal([]byte(raw), &manifest); err != nil {
		return err
	}
	if len(manifest.Groups) != 1 || len(manifest.Groups[0].Files) == 0 {
		return fmt.Errorf("unexpected model manifest")
	}
	dir := os.Args[1]
	if err = os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	client := http.Client{Timeout: 10 * time.Minute}
	for _, file := range manifest.Groups[0].Files {
		parsed, err := url.Parse(file.URL)
		if err != nil || parsed.Scheme != "https" || parsed.Host != "download.moonshine.ai" || filepath.Base(file.Name) != file.Name || file.Name == "." || file.Size <= 0 || file.ChecksumType != "crc32c" || file.Checksum == "" {
			return fmt.Errorf("invalid manifest entry %q", file.Name)
		}
		fmt.Println("Downloading", file.Name)
		response, err := client.Get(file.URL)
		if err != nil {
			return err
		}
		if response.StatusCode != 200 {
			response.Body.Close()
			return fmt.Errorf("download %s: HTTP %d", file.Name, response.StatusCode)
		}
		temp, err := os.CreateTemp(dir, ".download-*")
		if err != nil {
			response.Body.Close()
			return err
		}
		hash := crc32.New(crc32.MakeTable(crc32.Castagnoli))
		n, copyErr := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(response.Body, file.Size+1))
		response.Body.Close()
		closeErr := temp.Close()
		sum := make([]byte, 4)
		binary.BigEndian.PutUint32(sum, hash.Sum32())
		if copyErr != nil || closeErr != nil || n != file.Size || base64.StdEncoding.EncodeToString(sum) != file.Checksum {
			os.Remove(temp.Name())
			return fmt.Errorf("download verification failed for %s", file.Name)
		}
		if err = os.Chmod(temp.Name(), 0644); err != nil {
			os.Remove(temp.Name())
			return err
		}
		if err = os.Rename(temp.Name(), filepath.Join(dir, file.Name)); err != nil {
			os.Remove(temp.Name())
			return err
		}
	}
	return nil
}
