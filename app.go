package main

import (
	"encoding/json"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chai2010/webp"
	"github.com/google/uuid"
)

const (
	AuthToken     = "secrettoken"
	UploadDir     = "./uploads" // Changed to relative path for better portability
	Port          = ":4000"
	BaseURL       = "http://localhost" + Port
	MaxFileSize   = 50 << 20 // 100 MB limit
	MaxMemory     = 32 << 20 // 32 MB in memory before writing to disk
	WebPQuality   = 80       // WebP quality (0-100)
	ConvertToWebP = true     // Enable/disable WebP conversion
)

// Statistics struct for better organization
type Stats struct {
	Uploads int `json:"uploads"`
	Gets    int `json:"gets"`
	Deletes int `json:"deletes"`
}

var stats Stats

// Response structs for consistent JSON responses
type UploadResponse struct {
	URL         string `json:"url"`
	Filename    string `json:"filename"`
	OriginalExt string `json:"original_extension,omitempty"`
	Size        int64  `json:"size"`
	Converted   bool   `json:"converted_to_webp"`
	Message     string `json:"message"`
}

type ErrorResponse struct {
	Error   string `json:"error"`
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type FileListResponse struct {
	Files []FileInfo `json:"files"`
	Count int        `json:"count"`
}

type FileInfo struct {
	Name     string    `json:"name"`
	Size     int64     `json:"size"`
	Modified time.Time `json:"modified"`
	URL      string    `json:"url"`
}

type StatsResponse struct {
	Statistics Stats  `json:"statistics"`
	Uptime     string `json:"uptime"`
}

var startTime time.Time

func main() {
	startTime = time.Now()

	// Create upload directory with proper error handling
	absUploadDir, err := filepath.Abs(UploadDir)
	if err != nil {
		log.Fatal("Failed to get absolute path:", err)
	}

	if err := os.MkdirAll(absUploadDir, 0755); err != nil {
		log.Fatal("Failed to create upload directory:", err)
	}

	fmt.Printf("Upload directory: %s\n", absUploadDir)
	fmt.Printf("Max file size: %d MB\n", MaxFileSize/(1<<20))
	fmt.Printf("Max memory for uploads: %d MB\n", MaxMemory/(1<<20))

	// List existing files on startup
	listExistingFiles(absUploadDir)

	// Create custom server with increased limits
	server := &http.Server{
		Addr:           Port,
		Handler:        loggingMiddleware(http.DefaultServeMux),
		ReadTimeout:    60 * time.Second, // Increased timeout
		WriteTimeout:   60 * time.Second, // Increased timeout
		MaxHeaderBytes: 10 << 20,         // 10 MB for headers
	}

	// Routes
	http.HandleFunc("/", handleRoot)
	http.HandleFunc("/upload", withAuth(handleUpload))
	http.HandleFunc("/uploads/", handleServeFile)
	http.HandleFunc("/get/", handleServeFile) // Alternative endpoint
	http.HandleFunc("/files", handleFileList)
	http.HandleFunc("/delete/", withAuth(handleDelete))
	http.HandleFunc("/stats", handleStats)
	http.HandleFunc("/health", handleHealth)

	fmt.Printf("S3 Clone Server starting at %s\n", BaseURL)
	fmt.Println("Upload endpoint: POST /upload (requires auth)")
	if ConvertToWebP {
		fmt.Printf("WebP conversion: ENABLED (quality: %d%%)\n", WebPQuality)
	} else {
		fmt.Println("WebP conversion: DISABLED")
	}
	fmt.Println("Download endpoint: GET /uploads/<filename>")
	fmt.Println("List files: GET /files")
	fmt.Println("Delete file: DELETE /delete/<filename> (requires auth)")
	fmt.Println("Statistics: GET /stats")
	fmt.Println("Health check: GET /health")

	log.Fatal(server.ListenAndServe())
}

func listExistingFiles(dir string) {
	files, err := os.ReadDir(dir)
	if err != nil {
		fmt.Printf("Warning: Could not read upload directory: %v\n", err)
		return
	}

	if len(files) == 0 {
		fmt.Println("Upload directory is empty")
		return
	}

	fmt.Printf("Found %d existing files:\n", len(files))
	for _, file := range files {
		if !file.IsDir() {
			info, _ := file.Info()
			fmt.Printf("   - %s (%d bytes)\n", file.Name(), info.Size())
		}
	}
}

// Middleware for logging requests
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		fmt.Printf("[%s] %s %s - %v\n",
			start.Format("15:04:05"),
			r.Method,
			r.URL.Path,
			time.Since(start))
	})
}

// Authentication middleware
func withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+AuthToken {
			sendErrorResponse(w, "Unauthorized", http.StatusUnauthorized, "Invalid or missing authorization token")
			return
		}
		next(w, r)
	}
}

// Root endpoint with API documentation
func handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	response := map[string]interface{}{
		"name":    "S3 Clone Server",
		"version": "1.0.0",
		"endpoints": map[string]string{
			"POST /upload":          "Upload a file (requires Bearer token)",
			"GET /uploads/<file>":   "Download a file",
			"GET /files":            "List all files",
			"DELETE /delete/<file>": "Delete a file (requires Bearer token)",
			"GET /stats":            "Get server statistics",
			"GET /health":           "Health check",
		},
		"auth_header": "Authorization: Bearer " + AuthToken,
	}

	json.NewEncoder(w).Encode(response)
}

// Unified file serving handler for both /uploads/ and /get/ routes
func handleServeFile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendErrorResponse(w, "Method not allowed", http.StatusMethodNotAllowed, "Use GET method")
		return
	}

	var filename string
	if strings.HasPrefix(r.URL.Path, "/uploads/") {
		filename = strings.TrimPrefix(r.URL.Path, "/uploads/")
	} else if strings.HasPrefix(r.URL.Path, "/get/") {
		filename = strings.TrimPrefix(r.URL.Path, "/get/")
	} else {
		sendErrorResponse(w, "Invalid path", http.StatusBadRequest, "Invalid file path")
		return
	}

	if filename == "" || strings.Contains(filename, "..") || strings.Contains(filename, "/") {
		sendErrorResponse(w, "Invalid filename", http.StatusBadRequest, "Filename contains invalid characters")
		return
	}

	filePath := filepath.Join(UploadDir, filename)

	// Check if file exists and get info
	fileInfo, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		sendErrorResponse(w, "File not found", http.StatusNotFound, fmt.Sprintf("File '%s' does not exist", filename))
		return
	}
	if err != nil {
		sendErrorResponse(w, "File access error", http.StatusInternalServerError, "Could not access file")
		return
	}

	// Set appropriate headers
	w.Header().Set("Content-Length", fmt.Sprintf("%d", fileInfo.Size()))
	w.Header().Set("Last-Modified", fileInfo.ModTime().UTC().Format(http.TimeFormat))

	// Serve the file
	http.ServeFile(w, r, filePath)

	stats.Gets++
	fmt.Printf("Served file: %s (%d bytes)\n", filename, fileInfo.Size())
}

// Handle file uploads
func handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendErrorResponse(w, "Method not allowed", http.StatusMethodNotAllowed, "Use POST method")
		return
	}

	// Set a reasonable content length limit (with overhead for multipart)
	maxRequestSize := MaxFileSize + (10 << 20) // Add 10MB for multipart form overhead
	r.Body = http.MaxBytesReader(w, r.Body, int64(maxRequestSize))

	// Parse multipart form with increased memory limit
	if err := r.ParseMultipartForm(MaxMemory); err != nil {
		// Check if it's a size limit error
		errStr := err.Error()
		if strings.Contains(errStr, "too large") ||
			strings.Contains(errStr, "multipart: message too large") ||
			strings.Contains(errStr, "http: request body too large") {
			sendErrorResponse(w, "File too large", http.StatusRequestEntityTooLarge,
				fmt.Sprintf("Request size exceeds limit. Max file size: %d MB", MaxFileSize/(1<<20)))
		} else {
			sendErrorResponse(w, "Invalid form", http.StatusBadRequest, "Form data invalid: "+errStr)
		}
		return
	}

	file, handler, err := r.FormFile("file")
	if err != nil {
		sendErrorResponse(w, "No file provided", http.StatusBadRequest, "No file found in form data")
		return
	}
	defer file.Close()

	// Additional file size validation
	if handler.Size > MaxFileSize {
		sendErrorResponse(w, "File too large", http.StatusRequestEntityTooLarge,
			fmt.Sprintf("File size (%d bytes = %.2f MB) exceeds %d MB limit",
				handler.Size, float64(handler.Size)/(1<<20), MaxFileSize/(1<<20)))
		return
	}

	// Generate unique filename
	originalExt := strings.ToLower(filepath.Ext(handler.Filename))
	id := uuid.New().String()

	var filename string
	var converted bool
	var finalSize int64

	// Check if file should be converted to WebP
	if ConvertToWebP && isImageFile(originalExt) {
		webpFilename := id + ".webp"
		webpPath := filepath.Join(UploadDir, webpFilename)

		convertedSize, err := convertToWebP(file, webpPath, originalExt)
		if err != nil {
			fmt.Printf("WebP conversion failed for %s: %v, saving original\n", handler.Filename, err)
			// Fall back to saving original file
			filename = id + originalExt
			finalSize = saveOriginalFile(file, filepath.Join(UploadDir, filename))
		} else {
			filename = webpFilename
			finalSize = convertedSize
			converted = true
		}
	} else {
		// Save original file
		filename = id + originalExt
		finalSize = saveOriginalFile(file, filepath.Join(UploadDir, filename))
	}

	if finalSize == 0 {
		sendErrorResponse(w, "Save failed", http.StatusInternalServerError, "Could not save file")
		return
	}

	url := fmt.Sprintf("%s/uploads/%s", BaseURL, filename)
	response := UploadResponse{
		URL:         url,
		Filename:    filename,
		OriginalExt: originalExt,
		Size:        finalSize,
		Converted:   converted,
		Message:     "File uploaded successfully",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(response)

	stats.Uploads++
	if converted {
		fmt.Printf("Uploaded & converted: %s -> %s (%d bytes, WebP)\n", handler.Filename, filename, finalSize)
	} else {
		fmt.Printf("Uploaded: %s -> %s (%d bytes)\n", handler.Filename, filename, finalSize)
	}
}

// Handle file listing
func handleFileList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendErrorResponse(w, "Method not allowed", http.StatusMethodNotAllowed, "Use GET method")
		return
	}

	files, err := os.ReadDir(UploadDir)
	if err != nil {
		sendErrorResponse(w, "Directory read failed", http.StatusInternalServerError, "Could not read upload directory")
		return
	}

	var fileList []FileInfo
	for _, file := range files {
		if !file.IsDir() {
			info, err := file.Info()
			if err != nil {
				continue // Skip files with errors
			}

			fileInfo := FileInfo{
				Name:     file.Name(),
				Size:     info.Size(),
				Modified: info.ModTime(),
				URL:      fmt.Sprintf("%s/uploads/%s", BaseURL, file.Name()),
			}
			fileList = append(fileList, fileInfo)
		}
	}

	response := FileListResponse{
		Files: fileList,
		Count: len(fileList),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)

	fmt.Printf("Listed %d files\n", len(fileList))
}

// Handle file deletion
func handleDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		sendErrorResponse(w, "Method not allowed", http.StatusMethodNotAllowed, "Use DELETE method")
		return
	}

	filename := strings.TrimPrefix(r.URL.Path, "/delete/")
	if filename == "" || strings.Contains(filename, "..") || strings.Contains(filename, "/") {
		sendErrorResponse(w, "Invalid filename", http.StatusBadRequest, "Filename contains invalid characters")
		return
	}

	filePath := filepath.Join(UploadDir, filename)

	// Check if file exists
	fileInfo, err := os.Stat(filePath)
	if os.IsNotExist(err) {
		sendErrorResponse(w, "File not found", http.StatusNotFound, fmt.Sprintf("File '%s' does not exist", filename))
		return
	}
	if err != nil {
		sendErrorResponse(w, "File access error", http.StatusInternalServerError, "Could not access file")
		return
	}

	// Delete the file
	if err := os.Remove(filePath); err != nil {
		sendErrorResponse(w, "Delete failed", http.StatusInternalServerError, "Could not delete file")
		return
	}

	response := map[string]interface{}{
		"message":  "File deleted successfully",
		"filename": filename,
		"size":     fileInfo.Size(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)

	stats.Deletes++
	fmt.Printf("Deleted: %s (%d bytes)\n", filename, fileInfo.Size())
}

// Handle statistics
func handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendErrorResponse(w, "Method not allowed", http.StatusMethodNotAllowed, "Use GET method")
		return
	}

	uptime := time.Since(startTime).Round(time.Second)
	response := StatsResponse{
		Statistics: stats,
		Uptime:     uptime.String(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Handle health check
func handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		sendErrorResponse(w, "Method not allowed", http.StatusMethodNotAllowed, "Use GET method")
		return
	}

	// Check if upload directory is accessible
	_, err := os.Stat(UploadDir)
	if err != nil {
		sendErrorResponse(w, "Unhealthy", http.StatusServiceUnavailable, "Upload directory not accessible")
		return
	}

	response := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().UTC(),
		"uptime":    time.Since(startTime).Round(time.Second).String(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Helper function to send consistent error responses
func sendErrorResponse(w http.ResponseWriter, error string, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	response := ErrorResponse{
		Error:   error,
		Code:    code,
		Message: message,
	}

	json.NewEncoder(w).Encode(response)
}

// Check if file extension is an image that can be converted
func isImageFile(ext string) bool {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".gif":
		return true
	default:
		return false
	}
}

// Convert image to WebP format
func convertToWebP(src io.Reader, outputPath string, originalExt string) (int64, error) {
	// Reset file pointer if it's a file
	if seeker, ok := src.(io.Seeker); ok {
		seeker.Seek(0, 0)
	}

	var img image.Image
	var err error

	// Decode based on original format
	switch originalExt {
	case ".jpg", ".jpeg":
		img, err = jpeg.Decode(src)
	case ".png":
		img, err = png.Decode(src)
	case ".gif":
		img, err = gif.Decode(src)
	default:
		return 0, fmt.Errorf("unsupported format: %s", originalExt)
	}

	if err != nil {
		return 0, fmt.Errorf("failed to decode image: %w", err)
	}

	// Create output file
	dst, err := os.Create(outputPath)
	if err != nil {
		return 0, fmt.Errorf("failed to create output file: %w", err)
	}
	defer dst.Close()

	// Encode as WebP
	options := &webp.Options{
		Lossless: false,
		Quality:  WebPQuality,
	}

	if err := webp.Encode(dst, img, options); err != nil {
		os.Remove(outputPath) // Clean up on error
		return 0, fmt.Errorf("failed to encode WebP: %w", err)
	}

	// Get file size
	fileInfo, err := dst.Stat()
	if err != nil {
		return 0, fmt.Errorf("failed to get file info: %w", err)
	}

	return fileInfo.Size(), nil
}

// Save original file without conversion
func saveOriginalFile(src io.Reader, outputPath string) int64 {
	// Reset file pointer if it's a file
	if seeker, ok := src.(io.Seeker); ok {
		seeker.Seek(0, 0)
	}

	dst, err := os.Create(outputPath)
	if err != nil {
		fmt.Printf("Error creating file: %v\n", err)
		return 0
	}
	defer dst.Close()

	written, err := io.Copy(dst, src)
	if err != nil {
		os.Remove(outputPath) // Clean up on error
		fmt.Printf("Error copying file: %v\n", err)
		return 0
	}

	return written
}
