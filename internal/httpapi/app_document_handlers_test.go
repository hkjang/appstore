package httpapi

import (
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
)

func TestSanitizeDocumentFileNameStripsPathsAndKeepsKorean(t *testing.T) {
	cases := []struct{ input, want string }{
		{`..\..\windows\system32\보안 가이드.pdf`, "보안 가이드.pdf"},
		{"/etc/passwd.txt", "passwd.txt"},
		{"  운영\t매뉴얼.docx  ", "운영 매뉴얼.docx"},
		{"quote\"name.pdf", "quotename.pdf"},
		{"line\nbreak.md", "line break.md"},
		{"...", ""},
		{"", ""},
	}
	for _, test := range cases {
		if got := sanitizeDocumentFileName(test.input); got != test.want {
			t.Errorf("sanitizeDocumentFileName(%q) = %q, want %q", test.input, got, test.want)
		}
	}
	long := strings.Repeat("가", 200) + ".pdf"
	if got := []rune(sanitizeDocumentFileName(long)); len(got) != 160 {
		t.Errorf("long name kept %d runes, want 160", len(got))
	}
}

func TestDocumentContentTypeFollowsTheExtension(t *testing.T) {
	if got, ok := documentContentType("가이드.PDF"); !ok || got != "application/pdf" {
		t.Errorf("uppercase .PDF = %q (%v), want application/pdf", got, ok)
	}
	if got, ok := documentContentType("manual.docx"); !ok ||
		got != "application/vnd.openxmlformats-officedocument.wordprocessingml.document" {
		t.Errorf("docx = %q (%v)", got, ok)
	}
	for _, name := range []string{"payload.exe", "page.html", "archive.zip", "noextension"} {
		if _, ok := documentContentType(name); ok {
			t.Errorf("documentContentType(%q) was accepted", name)
		}
	}
}

func TestDocumentTitleFallsBackToTheFileNameWithoutItsExtension(t *testing.T) {
	if got := documentTitle("", "운영 가이드.pdf"); got != "운영 가이드" {
		t.Errorf("empty title = %q", got)
	}
	if got := documentTitle("  설치 안내  ", "install.pdf"); got != "설치 안내" {
		t.Errorf("trimmed title = %q", got)
	}
	if got := []rune(documentTitle(strings.Repeat("나", 200), "a.pdf")); len(got) != 160 {
		t.Errorf("long title kept %d runes, want 160", len(got))
	}
}

// A Korean file name has to survive the round trip: browsers read filename*,
// while the quoted ASCII fallback has to stay a valid header value.
func TestContentDispositionCarriesBothFileNameForms(t *testing.T) {
	header := contentDisposition("운영 가이드.pdf")
	if !strings.HasPrefix(header, "attachment;") {
		t.Fatalf("disposition = %q, want an attachment", header)
	}
	encoded, found := strings.CutPrefix(header[strings.Index(header, "filename*=UTF-8''"):], "filename*=UTF-8''")
	if !found {
		t.Fatalf("disposition = %q, want a UTF-8 file name", header)
	}
	decoded, err := url.PathUnescape(encoded)
	if err != nil || decoded != "운영 가이드.pdf" {
		t.Fatalf("decoded file name = %q (%v)", decoded, err)
	}
	if strings.Contains(header, "운영") {
		t.Fatalf("disposition = %q, want the raw name only in the encoded form", header)
	}
	if ascii := contentDisposition("보안.pdf"); !strings.Contains(ascii, "filename=document.pdf") {
		t.Fatalf("a name with no ASCII left = %q, want a generic fallback", ascii)
	}
}

func TestWithDownloadURLsPointsAtTheOwningApp(t *testing.T) {
	appID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	documentID := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	documents := withDownloadURLs([]model.AppDocument{{ID: documentID, AppID: appID}})
	want := "/api/v1/apps/" + appID.String() + "/documents/" + documentID.String()
	if documents[0].DownloadURL != want {
		t.Fatalf("downloadUrl = %q, want %q", documents[0].DownloadURL, want)
	}
}

func TestOwnsAppRequiresASignedInOwner(t *testing.T) {
	ownerID := uuid.New()
	owned := model.App{OwnerID: &ownerID}
	owner := &Principal{User: model.User{ID: ownerID}}
	other := &Principal{User: model.User{ID: uuid.New()}}
	if !ownsApp(owner, owned) {
		t.Error("the owner was not recognized")
	}
	if ownsApp(other, owned) {
		t.Error("another signed-in user was treated as the owner")
	}
	if ownsApp(nil, owned) {
		t.Error("an anonymous visitor was treated as the owner")
	}
	if ownsApp(owner, model.App{}) {
		t.Error("an app with no owner matched a signed-in user")
	}
}
