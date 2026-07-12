package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/go-chi/chi"
	"github.com/h2non/filetype"
	"github.com/icza/dyno"
	"github.com/subosito/gozaru"
	"gopkg.in/yaml.v3"
)

var rootUploadPath = "/app/files/"

type FileResponse struct {
	URL      string `json:"url"`
	Filename string `json:"filename"`
	FileType string `json:"filetype"`
}

var maxBytesReader *http.MaxBytesError

func serveFile(w http.ResponseWriter, r *http.Request) {
	fileId := chi.URLParam(r, "fileid")

	metadataFilePath := filepath.Join(rootUploadPath, fileId[:2], fileId[2:4], fileId+".yaml")
	metadataFile, err := os.ReadFile(metadataFilePath)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	var metaData map[string]any
	if err := yaml.Unmarshal(metadataFile, &metaData); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	if delete := metaData["delete"].(bool); delete {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	fileHash, _ := dyno.GetString(metaData["hash"])
	filePath := filepath.Join(rootUploadPath, fileHash[:2], fileHash[2:4], fileHash)
	originalFileName, _ := dyno.GetString(metaData["filename"])

	w.Header().Set("Content-Disposition", `attachment; filename*=UTF-8''`+url.QueryEscape(originalFileName))
	http.ServeFile(w, r, filePath)
}

func uploadFile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, int64(settingConfig.MaxFileSize)<<20)

	file, handler, err := r.FormFile("file")
	if err != nil {
		if errors.As(err, &maxBytesReader) {
			http.Error(w, "File too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "error", http.StatusBadRequest)
		return
	}
	defer file.Close()

	if err := os.MkdirAll(rootUploadPath, os.ModePerm); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	head := make([]byte, 512)
	file.Read(head)

	t, _ := filetype.Match(head)

	file.Seek(0, io.SeekStart)

	// For JPEG images under a sane size, try to shrink them (resize if very
	// large + re-encode at slightly reduced quality) to save storage space.
	// Falls back silently to the original file on any decode/encode issue.
	var uploadReader io.ReadSeeker = file
	if t.MIME.Type == "image/jpeg" && handler.Size > 0 && handler.Size < 15<<20 {
		if original, readErr := io.ReadAll(file); readErr == nil {
			if compressed, ok := compressJPEGIfLarger(original, 1920, 82); ok && len(compressed) < len(original) {
				uploadReader = bytes.NewReader(compressed)
			} else {
				uploadReader = bytes.NewReader(original)
			}
		} else {
			file.Seek(0, io.SeekStart)
		}
	}

	fileHash, err := generatedFileHash(uploadReader)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	hashSubDir := filepath.Join(rootUploadPath, fileHash[:2], fileHash[2:4])
	if err := os.MkdirAll(hashSubDir, os.ModePerm); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	var isDuplicateFile bool
	testISDuplicateFilePath := filepath.Join(hashSubDir, fileHash)
	_, err = os.Stat(testISDuplicateFilePath)
	if err == nil { //|| !os.IsNotExist(err)
		isDuplicateFile = true
	}

	if !isDuplicateFile {
		destPath := filepath.Join(hashSubDir, fileHash)

		uploadReader.Seek(0, io.SeekStart)
		destFile, err := os.Create(destPath)
		if err != nil {
			http.Error(w, "error", http.StatusInternalServerError)
			return
		}
		defer destFile.Close()

		if _, err := io.Copy(destFile, uploadReader); err != nil {
			http.Error(w, "error", http.StatusInternalServerError)
			return
		}
	}

	id := generatedRandomID(20)
	if id == "" {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	yamlFileDir := filepath.Join(rootUploadPath, id[:2], id[2:4])
	if err := os.MkdirAll(yamlFileDir, os.ModePerm); err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	safeFilename := gozaru.Sanitize(handler.Filename)

	fileMetadata := map[string]any{
		"id":       id,
		"filename": safeFilename,
		"hash":     fileHash,
		"type":     t.MIME.Type,
		"delete":   false,
	}
	metadataFilePath := filepath.Join(rootUploadPath, id[:2], id[2:4], id+".yaml")
	metadataFile, err := os.Create(metadataFilePath)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	defer metadataFile.Close()

	yamlData, err := yaml.Marshal(fileMetadata)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	metadataFile.Write(yamlData)

	fileUrl := "/api/files/" + id

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(FileResponse{
		URL:      fileUrl,
		Filename: handler.Filename,
		FileType: t.MIME.Type,
	})
}

// compressJPEGIfLarger downsizes a JPEG to fit within maxDim (on its longest
// side) if it's larger, and always re-encodes it at the given quality.
// Returns ok=false if the image can't be decoded, in which case the caller
// should keep the original bytes untouched.
func compressJPEGIfLarger(data []byte, maxDim int, quality int) ([]byte, bool) {
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}

	bounds := img.Bounds()
	if bounds.Dx() > maxDim || bounds.Dy() > maxDim {
		img = resizeNearestNeighbor(img, maxDim)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, false
	}

	return buf.Bytes(), true
}

// resizeNearestNeighbor scales an image down so its longest side is maxDim,
// preserving aspect ratio. Simple nearest-neighbor sampling, no external
// dependencies. Returns the source unchanged if it's already small enough.
func resizeNearestNeighbor(src image.Image, maxDim int) image.Image {
	bounds := src.Bounds()
	srcW := bounds.Dx()
	srcH := bounds.Dy()
	if srcW == 0 || srcH == 0 {
		return src
	}

	scale := float64(maxDim) / float64(srcW)
	if srcH > srcW {
		scale = float64(maxDim) / float64(srcH)
	}
	if scale >= 1 {
		return src
	}

	dstW := int(float64(srcW) * scale)
	dstH := int(float64(srcH) * scale)
	if dstW < 1 {
		dstW = 1
	}
	if dstH < 1 {
		dstH = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, dstW, dstH))
	for y := 0; y < dstH; y++ {
		srcY := bounds.Min.Y + y*srcH/dstH
		for x := 0; x < dstW; x++ {
			srcX := bounds.Min.X + x*srcW/dstW
			dst.Set(x, y, src.At(srcX, srcY))
		}
	}
	return dst
}

func generatedFileHash(file io.Reader) (string, error) {
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

func generatedRandomID(len int) string {
	b := make([]byte, len)
	_, err := rand.Read(b)
	if err != nil {
		return ""
	}

	return hex.EncodeToString(b)
}

// TODO: Image size limitation
func getFavicon(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := getChannelDetails(ctx)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}

	logoUrl := c["logoUrl"]
	if logoUrl == "" {
		logoUrl = "assets/favicon.ico"
	}
	fileId := path.Base(logoUrl)

	metadataFilePath := filepath.Join(rootUploadPath, fileId[:2], fileId[2:4], fileId+".yaml")
	metadataFile, err := os.ReadFile(metadataFilePath)
	if err != nil {
		http.ServeFile(w, r, "assets/favicon.ico")
		return
	}

	var metaData map[string]any
	if err := yaml.Unmarshal(metadataFile, &metaData); err != nil {
		http.ServeFile(w, r, "assets/favicon.ico")
		return
	}

	if delete := metaData["delete"].(bool); delete {
		http.ServeFile(w, r, "assets/favicon.ico")
		return
	}

	fileHash, _ := dyno.GetString(metaData["hash"])
	filePath := filepath.Join(rootUploadPath, fileHash[:2], fileHash[2:4], fileHash)

	http.ServeFile(w, r, filePath)
}
