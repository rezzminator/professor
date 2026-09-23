package harvestpy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// BrowserDownloadRequest is one browser download: the fetch request's
// navigation and dial, plus the path Go names for the file and the byte cap.
// The worker streams the file into Path and stops at MaxBytes.
type BrowserDownloadRequest struct {
	BrowserFetchRequest
	Path     string `json:"path"`
	MaxBytes int64  `json:"max_bytes"`
}

// BrowserDownload is what the worker wrote to the request's Path. Via names
// how it arrived: "download" (the browser's download event) or "response"
// (the navigation's own response body).
type BrowserDownload struct {
	Bytes       int64  `json:"bytes"`
	ContentType string `json:"content_type"`
	FinalURL    string `json:"final_url"`
	Status      int    `json:"status"`
	Via         string `json:"via"`
}

// BrowserDownloadFailure is a download that ran and ended without the file for
// a named Reason ("no-download", "too-large", "timeout"); Status and Head are
// the page the navigation showed instead, when it showed one.
type BrowserDownloadFailure struct {
	Reason  string
	Status  int
	Head    string
	Message string
}

func (failure *BrowserDownloadFailure) Error() string {
	return fmt.Sprintf("browser download %s: %s", failure.Reason, failure.Message)
}

// Download runs the worker's download op: navigate to the URL, capture the
// browser's download or the navigation's response body into request.Path,
// capped at request.MaxBytes. Every URL Chrome touches arrives as an ask
// (onAsk, as in FetchPinned). A named failure is a *BrowserDownloadFailure;
// any other error is a download that could not run.
func (worker *BrowserWorker) Download(
	ctx context.Context,
	request BrowserDownloadRequest,
	onAsk func(url string) error,
) (BrowserDownload, error) {
	if strings.TrimSpace(request.URL) == "" || strings.TrimSpace(request.Path) == "" || request.MaxBytes <= 0 {
		return BrowserDownload{}, fmt.Errorf(
			"browser download needs a url, a path and a positive cap (url %q, path %q, cap %d)",
			request.URL, request.Path, request.MaxBytes)
	}
	body, err := json.Marshal(struct {
		Op string `json:"op"`
		BrowserDownloadRequest
	}{Op: "download", BrowserDownloadRequest: request})
	if err != nil {
		return BrowserDownload{}, fmt.Errorf("marshal browser download request: %w", err)
	}
	line, stderr, err := worker.requestInteractive(ctx, "download", body, onAsk)
	if err != nil {
		return BrowserDownload{}, err
	}
	var response struct {
		BrowserDownload
		OK     bool   `json:"ok"`
		Error  string `json:"error"`
		Reason string `json:"reason"`
		Head   string `json:"head"`
	}
	if err := json.Unmarshal(line, &response); err != nil {
		return BrowserDownload{}, fmt.Errorf("decode browser download response JSON: %w (stderr: %s)", err, stderr)
	}
	if response.OK {
		return response.BrowserDownload, nil
	}
	if response.Error == "" {
		response.Error = "browser worker returned ok=false without error"
	}
	if response.Reason != "" {
		return BrowserDownload{}, &BrowserDownloadFailure{
			Reason: response.Reason, Status: response.Status, Head: response.Head, Message: response.Error,
		}
	}
	return BrowserDownload{}, errors.New(response.Error)
}
