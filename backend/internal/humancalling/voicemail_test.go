package humancalling_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/chasef07/acuity_product/backend/internal/humancalling"
)

func TestRecordingDownloaderRevalidatesRedirects(t *testing.T) {
	var requests atomic.Int32
	client := &http.Client{
		Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			requests.Add(1)
			return &http.Response{
				StatusCode: http.StatusFound,
				Header: http.Header{
					"Location": {"https://169.254.169.254/latest/meta-data"},
				},
				Body:    http.NoBody,
				Request: request,
			}, nil
		}),
	}
	downloader := humancalling.NewHTTPRecordingDownloader(
		client,
		"recordings.telnyx.test",
	)
	if _, _, err := downloader.Download(
		context.Background(),
		"https://recordings.telnyx.test/voicemail.wav",
	); err == nil {
		t.Fatal("private-address redirect was accepted")
	}
	if requests.Load() != 1 {
		t.Fatalf("redirect requests = %d, want one", requests.Load())
	}
}

func TestRecordingDownloaderAllowsOnlyConfiguredHTTPSHost(t *testing.T) {
	client := &http.Client{
		Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header: http.Header{
					"Content-Type": {"audio/wav"},
				},
				Body:    http.NoBody,
				Request: request,
			}, nil
		}),
	}
	downloader := humancalling.NewHTTPRecordingDownloader(
		client,
		"recordings.telnyx.test",
	)
	for _, recordingURL := range []string{
		"http://recordings.telnyx.test/voicemail.wav",
		"https://unexpected.example/voicemail.wav",
		"https://127.0.0.1/voicemail.wav",
	} {
		if _, _, err := downloader.Download(
			context.Background(),
			recordingURL,
		); err == nil {
			t.Fatalf("unsafe recording URL %q was accepted", recordingURL)
		}
	}

	success := humancalling.NewHTTPRecordingDownloader(
		&http.Client{
			Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Header: http.Header{
						"Content-Type": {"audio/wav; charset=binary"},
					},
					Body:    io.NopCloser(strings.NewReader("audio")),
					Request: request,
				}, nil
			}),
		},
		"recordings.telnyx.test",
	)
	content, contentType, err := success.Download(
		context.Background(),
		"https://recordings.telnyx.test/voicemail.wav",
	)
	if err != nil || string(content) != "audio" || contentType != "audio/wav" {
		t.Fatalf(
			"recording response = content %q type %q err %v",
			content,
			contentType,
			err,
		)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (transport roundTripperFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return transport(request)
}
