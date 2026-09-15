package guides

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/store"
)

// fakeRepository holds apps by slug and documents by app, mirroring the two
// rules the real store enforces: ten files per app and one name per app.
type fakeRepository struct {
	apps      map[string]model.App
	documents map[uuid.UUID][]model.AppDocument
	content   map[uuid.UUID][]byte
	calls     []string
}

func newFakeRepository(slugs ...string) *fakeRepository {
	r := &fakeRepository{
		apps:      map[string]model.App{},
		documents: map[uuid.UUID][]model.AppDocument{},
		content:   map[uuid.UUID][]byte{},
	}
	for _, slug := range slugs {
		r.apps[slug] = model.App{ID: uuid.New(), Slug: slug}
	}
	return r
}

func (r *fakeRepository) GetAppBySlugFold(_ context.Context, slug string) (model.App, error) {
	for stored, app := range r.apps {
		if strings.EqualFold(stored, slug) {
			return app, nil
		}
	}
	return model.App{}, fmt.Errorf("get app by slug: %w", store.ErrNotFound)
}

func (r *fakeRepository) ListAppDocuments(_ context.Context, appID uuid.UUID) ([]model.AppDocument, error) {
	return append([]model.AppDocument(nil), r.documents[appID]...), nil
}

func (r *fakeRepository) CreateAppDocument(_ context.Context, appID uuid.UUID, input store.AppDocumentInput) (model.AppDocument, error) {
	r.calls = append(r.calls, "create "+input.FileName)
	if len(r.documents[appID]) >= store.MaxAppDocuments {
		return model.AppDocument{}, fmt.Errorf("app document limit: %w", store.ErrConflict)
	}
	for _, document := range r.documents[appID] {
		if strings.EqualFold(document.FileName, input.FileName) {
			return model.AppDocument{}, fmt.Errorf("create app document: %w", store.ErrConflict)
		}
	}
	document := model.AppDocument{
		ID: uuid.New(), AppID: appID, Title: input.Title, FileName: input.FileName,
		ContentType: input.ContentType, Size: len(input.Content), Checksum: checksumOf(input.Content),
	}
	r.documents[appID] = append(r.documents[appID], document)
	r.content[document.ID] = input.Content
	return document, nil
}

func (r *fakeRepository) ReplaceAppDocument(_ context.Context, appID, documentID uuid.UUID, input store.AppDocumentInput) (model.AppDocument, error) {
	r.calls = append(r.calls, "replace "+input.FileName)
	for index, document := range r.documents[appID] {
		if document.ID == documentID {
			document.Title, document.FileName, document.ContentType = input.Title, input.FileName, input.ContentType
			document.Size, document.Checksum = len(input.Content), checksumOf(input.Content)
			r.documents[appID][index] = document
			r.content[documentID] = input.Content
			return document, nil
		}
	}
	return model.AppDocument{}, fmt.Errorf("replace app document: %w", store.ErrNotFound)
}

func checksumOf(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func bundle(files map[string]string) fstest.MapFS {
	m := fstest.MapFS{}
	for name, content := range files {
		m[name] = &fstest.MapFile{Data: []byte(content)}
	}
	return m
}

func TestSyncAttachesOnlyToTheAppOfTheSameSlug(t *testing.T) {
	repository := newFakeRepository("dataworks", "dataworks-v2")
	summary, err := Sync(context.Background(), repository, bundle(map[string]string{
		"dataworks/USER_GUIDE.pdf":   "%PDF user",
		"dataworks/ADMIN_GUIDE.pdf":  "%PDF admin",
		"unknown-app/USER_GUIDE.pdf": "%PDF orphan",
	}), quiet())
	if err != nil {
		t.Fatal(err)
	}
	if summary != (Summary{Apps: 1, Unmatched: 1, Attached: 2}) {
		t.Fatalf("summary = %+v", summary)
	}
	documents := repository.documents[repository.apps["dataworks"].ID]
	if len(documents) != 2 {
		t.Fatalf("dataworks carries %d documents, want 2", len(documents))
	}
	if len(repository.documents[repository.apps["dataworks-v2"].ID]) != 0 {
		t.Fatal("a guide landed on an app with a merely similar slug")
	}
	titles := map[string]string{}
	for _, document := range documents {
		titles[document.FileName] = document.Title
		if document.ContentType != "application/pdf" {
			t.Errorf("%s stored as %q", document.FileName, document.ContentType)
		}
	}
	if titles["USER_GUIDE.pdf"] != "사용자 가이드" || titles["ADMIN_GUIDE.pdf"] != "관리자 가이드" {
		t.Errorf("titles = %v", titles)
	}
}

func TestSyncMatchesSlugsCaseInsensitively(t *testing.T) {
	repository := newFakeRepository("dataworks", "AgentHub")
	summary, err := Sync(context.Background(), repository, bundle(map[string]string{
		"DataWorks/USER_GUIDE.pdf": "%PDF user",
		"agenthub/USER_GUIDE.pdf":  "%PDF agent",
	}), quiet())
	if err != nil {
		t.Fatal(err)
	}
	if summary != (Summary{Apps: 2, Attached: 2}) {
		t.Fatalf("summary = %+v", summary)
	}
	for _, slug := range []string{"dataworks", "AgentHub"} {
		if len(repository.documents[repository.apps[slug].ID]) != 1 {
			t.Errorf("%s carries %d documents, want 1", slug, len(repository.documents[repository.apps[slug].ID]))
		}
	}
}

func TestSyncIsIdempotentAndReplacesOnlyChangedBytes(t *testing.T) {
	repository := newFakeRepository("dataworks")
	first := bundle(map[string]string{"dataworks/USER_GUIDE.pdf": "%PDF v1", "dataworks/ADMIN_GUIDE.pdf": "%PDF admin"})
	if _, err := Sync(context.Background(), repository, first, quiet()); err != nil {
		t.Fatal(err)
	}
	appID := repository.apps["dataworks"].ID
	var originalID uuid.UUID
	for _, document := range repository.documents[appID] {
		if document.FileName == "USER_GUIDE.pdf" {
			originalID = document.ID
		}
	}

	summary, err := Sync(context.Background(), repository, first, quiet())
	if err != nil {
		t.Fatal(err)
	}
	if summary != (Summary{Apps: 1, Unchanged: 2}) {
		t.Fatalf("second pass summary = %+v", summary)
	}

	second := bundle(map[string]string{"dataworks/USER_GUIDE.pdf": "%PDF v2", "dataworks/ADMIN_GUIDE.pdf": "%PDF admin"})
	summary, err = Sync(context.Background(), repository, second, quiet())
	if err != nil {
		t.Fatal(err)
	}
	if summary != (Summary{Apps: 1, Replaced: 1, Unchanged: 1}) {
		t.Fatalf("third pass summary = %+v", summary)
	}
	if len(repository.documents[appID]) != 2 {
		t.Fatalf("replacement left %d documents, want 2", len(repository.documents[appID]))
	}
	var replaced model.AppDocument
	for _, document := range repository.documents[appID] {
		if document.FileName == "USER_GUIDE.pdf" {
			replaced = document
		}
	}
	if replaced.ID != originalID {
		t.Error("replacement changed the document identifier")
	}
	if string(repository.content[replaced.ID]) != "%PDF v2" {
		t.Errorf("content after replacement = %q", repository.content[replaced.ID])
	}
}

func TestSyncLeavesOwnerUploadsAloneAndMatchesNamesCaseInsensitively(t *testing.T) {
	repository := newFakeRepository("dataworks")
	appID := repository.apps["dataworks"].ID
	if _, err := repository.CreateAppDocument(context.Background(), appID, store.AppDocumentInput{
		Title: "설치 메모", FileName: "notes.md", ContentType: "text/markdown; charset=utf-8", Content: []byte("# notes"),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateAppDocument(context.Background(), appID, store.AppDocumentInput{
		Title: "old", FileName: "user_guide.pdf", ContentType: "application/pdf", Content: []byte("%PDF old"),
	}); err != nil {
		t.Fatal(err)
	}
	summary, err := Sync(context.Background(), repository, bundle(map[string]string{"dataworks/USER_GUIDE.pdf": "%PDF new"}), quiet())
	if err != nil {
		t.Fatal(err)
	}
	if summary != (Summary{Apps: 1, Replaced: 1}) {
		t.Fatalf("summary = %+v", summary)
	}
	if len(repository.documents[appID]) != 2 {
		t.Fatalf("%d documents, want the owner's note plus the replaced guide", len(repository.documents[appID]))
	}
	for _, document := range repository.documents[appID] {
		if document.FileName == "notes.md" && document.Title != "설치 메모" {
			t.Error("the owner's own upload was touched")
		}
	}
}

func TestSyncSkipsFilesTheUploadHandlerWouldRefuse(t *testing.T) {
	repository := newFakeRepository("dataworks")
	summary, err := Sync(context.Background(), repository, bundle(map[string]string{
		"dataworks/USER_GUIDE.pdf": "%PDF ok",
		"dataworks/index.html":     "<script>",
		"dataworks/empty.pdf":      "",
		"dataworks/huge.pdf":       strings.Repeat("x", model.MaxAppDocumentBytes+1),
	}), quiet())
	if err != nil {
		t.Fatal(err)
	}
	if summary != (Summary{Apps: 1, Attached: 1, Failed: 3}) {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestSyncCountsAFullAppAsFailureAndKeepsGoing(t *testing.T) {
	repository := newFakeRepository("full", "dataworks")
	fullID := repository.apps["full"].ID
	for index := 0; index < store.MaxAppDocuments; index++ {
		if _, err := repository.CreateAppDocument(context.Background(), fullID, store.AppDocumentInput{
			FileName: fmt.Sprintf("owner-%d.pdf", index), ContentType: "application/pdf", Content: []byte("x"),
		}); err != nil {
			t.Fatal(err)
		}
	}
	summary, err := Sync(context.Background(), repository, bundle(map[string]string{
		"full/USER_GUIDE.pdf":      "%PDF",
		"dataworks/USER_GUIDE.pdf": "%PDF",
	}), quiet())
	if err != nil {
		t.Fatal(err)
	}
	if summary != (Summary{Apps: 2, Attached: 1, Failed: 1}) {
		t.Fatalf("summary = %+v", summary)
	}
}

func TestTitle(t *testing.T) {
	cases := map[string]string{
		"USER_GUIDE.pdf": "사용자 가이드",
		"admin_guide.md": "관리자 가이드",
		"릴리스 노트.pdf":     "릴리스 노트",
	}
	for fileName, want := range cases {
		if got := Title(fileName); got != want {
			t.Errorf("Title(%q) = %q, want %q", fileName, got, want)
		}
	}
}
