package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

const (
	AuthToken = "secrettoken"
	UploadDir = "./uploads"
	Port      = ":4000"
	BaseUrl   = "http://localhost" + Port
)

func main() {

	os.MkdirAll(UploadDir, os.ModePerm)

	http.Handle("/uploads/", http.StripPrefix("/uploads/", http.FileServer(http.Dir(UploadDir))))

	http.HandleFunc("/upload", withAuth(handleUpload))

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

}
