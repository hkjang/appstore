package model

import (
	"path"
	"strings"
)

// MaxAppDocumentBytes caps one guide file. A manual with screen captures fits
// comfortably; anything larger belongs on a file share, not in the catalog
// database that every backup copies. The upload handler and the bundled guide
// sync enforce the same number so a file accepted by one is never refused by
// the other.
const MaxAppDocumentBytes = 20 << 20

// AppDocumentContentTypes maps the extensions a guide may use to the type
// served back on download. The extension decides, not the browser's guess: the
// same .docx arrives as application/octet-stream from one client and as the
// Office type from another.
var AppDocumentContentTypes = map[string]string{
	".pdf":      "application/pdf",
	".md":       "text/markdown; charset=utf-8",
	".markdown": "text/markdown; charset=utf-8",
	".txt":      "text/plain; charset=utf-8",
	".csv":      "text/csv; charset=utf-8",
	".doc":      "application/msword",
	".docx":     "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".ppt":      "application/vnd.ms-powerpoint",
	".pptx":     "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".xls":      "application/vnd.ms-excel",
	".xlsx":     "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".hwp":      "application/x-hwp",
	".hwpx":     "application/hwp+zip",
}

// AppDocumentContentType resolves the stored content type from a file name
// and reports whether the extension is one a guide may carry.
func AppDocumentContentType(fileName string) (string, bool) {
	contentType, ok := AppDocumentContentTypes[strings.ToLower(path.Ext(fileName))]
	return contentType, ok
}
