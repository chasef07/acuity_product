package messaging

import "testing"

func TestAttachmentTypesRoundTripThroughDetectionAndFileNames(t *testing.T) {
	samples := map[string][]byte{
		"image/jpeg":      []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00"),
		"image/png":       []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"),
		"image/gif":       []byte("GIF89a\x01\x00\x01\x00"),
		"image/webp":      []byte("RIFF\x24\x00\x00\x00WEBPVP8 "),
		"application/pdf": []byte("%PDF-1.7\n"),
	}
	if len(samples) != len(attachmentExtensions) {
		t.Fatalf("samples cover %d types, table has %d", len(samples), len(attachmentExtensions))
	}
	for contentType, extensions := range attachmentExtensions {
		detected, err := detectAttachmentType(samples[contentType], maximumOutboundAttachmentBytes)
		if err != nil || detected != contentType {
			t.Fatalf("detect %s = %q, %v", contentType, detected, err)
		}
		fileName := AttachmentFileName(contentType)
		if fileName != "attachment"+extensions[0] || !fileNameMatchesType(fileName, contentType) {
			t.Fatalf("%s file name = %q", contentType, fileName)
		}
		for _, extension := range extensions {
			if !fileNameMatchesType("Scan"+extension, contentType) {
				t.Fatalf("%s rejects %s", contentType, extension)
			}
		}
	}
	if AttachmentFileName("text/html") != "attachment.pdf" || fileNameMatchesType("page.html", "text/html") {
		t.Fatal("unsupported type must keep the PDF fallback name and never match a file name")
	}
	if _, err := detectAttachmentType([]byte("<html></html>"), maximumOutboundAttachmentBytes); err == nil {
		t.Fatal("HTML content was accepted as an attachment")
	}
}
