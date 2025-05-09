package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

const (
	AuthToken = "secrettoken"
	// UploadDir = "./uploads"
	UploadDir = "D:/Code/s3Clone/uploads"
	Port      = ":4000"
	BaseUrl   = "http://localhost" + Port
)

var countUploads = 0
var countGet = 0
var countDeleted = 0

func main() {

	os.MkdirAll(UploadDir, os.ModePerm)

	http.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(UploadDir))))

	// http.HandleFunc("/get/", withAuth(handleGet))
	http.HandleFunc("/get", handleGet)
	http.HandleFunc("/get/all", handleGetAll)
	http.HandleFunc("/upload", withAuth(handleUpload))
	http.HandleFunc("/delete", handleDelete)

	fmt.Println("Server started at " + BaseUrl)
	log.Fatal(http.ListenAndServe(Port, nil))
}

func withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+AuthToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func handleGetAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Use GET", http.StatusMethodNotAllowed)
		return
	}

	files, err := os.ReadDir(UploadDir)
	if err != nil {
		http.Error(w, "Failed to read directory", http.StatusInternalServerError)
		return
	}

	var fileList []string
	for _, file := range files {
		fileList = append(fileList, file.Name())
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(fmt.Sprintf(`{"files": %v}`, fileList)))

	countGet++
	fmt.Println("Total GET requests:", countGet)
}

func handleDelete(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodDelete {
		http.Error(w, "Use DELETE", http.StatusMethodNotAllowed)
		return
	}

	filename := strings.TrimPrefix(r.URL.Path, "/delete/")
	if filename == "" || strings.Contains(filename, "..") {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}

	filePath := filepath.Join(UploadDir, filename)

	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	if err := os.Remove(filePath); err != nil {
		http.Error(w, "Failed to delete file", http.StatusInternalServerError)
		return
	}

	fmt.Println("File deleted:", filename)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("File deleted successfully"))

	countDeleted++
	fmt.Println("Total deleted files:", countDeleted)
}

func handleGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Use GET", http.StatusMethodNotAllowed)
		return
	}

	// Extract filename from path: /get/<filename>
	// Trim "/get/" from the path
	filename := strings.TrimPrefix(r.URL.Path, "/get/")
	if filename == "" || strings.Contains(filename, "..") {
		http.Error(w, "Invalid filename", http.StatusBadRequest)
		return
	}

	filePath := filepath.Join(UploadDir, filename)

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}

	fmt.Println("Serving file:", filePath)

	// Serve the file
	http.ServeFile(w, r, filePath)
	countGet++
	fmt.Println("Total GET requests:", countGet)
}

func handleUpload(w http.ResponseWriter, r *http.Request) {

	if r.Method != http.MethodPost {
		http.Error(w, "Use POST", http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseMultipartForm(10 << 20) // 10 MB limit
	if err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	file, handler, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Invalid or No file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	ext := filepath.Ext(handler.Filename)
	id := uuid.New().String()
	filename := id + ext
	dst, err := os.Create(filepath.Join(UploadDir, filename))
	if err != nil {
		http.Error(w, "Failed to save", http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	io.Copy(dst, file)

	fmt.Println("File saved:", filename)

	url := fmt.Sprintf("%s/uploads/%s", BaseUrl, filename)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(fmt.Appendf(nil, `{"url": "%s"}`, url))

	countUploads++
	fmt.Println("Total uploads:", countUploads)
}
