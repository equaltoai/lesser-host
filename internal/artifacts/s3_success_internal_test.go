package artifacts

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type memObject struct {
	body         []byte
	contentType  string
	cacheControl string
	etag         string
}

type memS3 struct {
	mu   sync.Mutex
	objs map[string]memObject
}

func (m *memS3) handler(w http.ResponseWriter, r *http.Request) {
	bucket, key, ok := parsePathStyle(r.URL.Path)
	if !ok || bucket == "" {
		http.NotFound(w, r)
		return
	}
	objKey := bucket + "/" + key

	switch r.Method {
	case http.MethodPut:
		body, _ := io.ReadAll(r.Body)
		_ = r.Body.Close()

		sum := sha256.Sum256(body)
		etag := `"` + hex.EncodeToString(sum[:]) + `"`

		m.mu.Lock()
		if m.objs == nil {
			m.objs = map[string]memObject{}
		}
		m.objs[objKey] = memObject{
			body:         body,
			contentType:  strings.TrimSpace(r.Header.Get("Content-Type")),
			cacheControl: strings.TrimSpace(r.Header.Get("Cache-Control")),
			etag:         etag,
		}
		m.mu.Unlock()

		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusOK)
		return

	case http.MethodGet:
		m.mu.Lock()
		obj, ok := m.objs[objKey]
		m.mu.Unlock()
		if !ok {
			writeNoSuchKey(w)
			return
		}
		if obj.contentType != "" {
			w.Header().Set("Content-Type", obj.contentType)
		}
		if obj.etag != "" {
			w.Header().Set("ETag", obj.etag)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(obj.body)
		return

	case http.MethodHead:
		m.mu.Lock()
		obj, ok := m.objs[objKey]
		m.mu.Unlock()
		if !ok {
			writeNoSuchKey(w)
			return
		}
		if obj.contentType != "" {
			w.Header().Set("Content-Type", obj.contentType)
		}
		if obj.etag != "" {
			w.Header().Set("ETag", obj.etag)
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(obj.body)))
		w.WriteHeader(http.StatusOK)
		return

	case http.MethodDelete:
		m.mu.Lock()
		delete(m.objs, objKey)
		m.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return

	default:
		http.NotFound(w, r)
		return
	}
}

func parsePathStyle(path string) (bucket string, key string, ok bool) {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return "", "", false
	}
	parts := strings.SplitN(path, "/", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func writeNoSuchKey(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusNotFound)
	_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<Error>
  <Code>NoSuchKey</Code>
  <Message>The specified key does not exist.</Message>
  <Key>missing</Key>
</Error>`)
}

func newTestStore(t *testing.T, bucket string) (*Store, func()) {
	t.Helper()

	mem := &memS3{}
	ts := httptest.NewServer(http.HandlerFunc(mem.handler))

	cfg := aws.Config{
		Region:      "us-east-1",
		Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(ts.URL)
		o.UsePathStyle = true
		o.HTTPClient = ts.Client()
	})

	st := NewWithClient(bucket, client)

	return st, ts.Close
}

func TestNewWithClient_NilClient_ReturnsStore(t *testing.T) {
	st := NewWithClient(" bucket ", nil)
	if st == nil {
		t.Fatalf("expected store")
	}
	if st.bucket != "bucket" {
		t.Fatalf("expected trimmed bucket, got %q", st.bucket)
	}
}

func TestStore_ObjectLifecycle_SucceedsWithoutAWS(t *testing.T) {
	ctx := context.Background()
	st, cleanup := newTestStore(t, "bucket")
	defer cleanup()

	if err := st.PutObject(ctx, "k1", []byte("hello"), " text/plain ", " max-age=60 "); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

	ct, etag, size, err := st.HeadObject(ctx, "k1")
	if err != nil {
		t.Fatalf("HeadObject: %v", err)
	}
	if ct != "text/plain" || etag == "" || size != int64(len("hello")) {
		t.Fatalf("unexpected head response: ct=%q etag=%q size=%d", ct, etag, size)
	}

	body, ct2, etag2, err := st.GetObject(ctx, "k1", 0)
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if string(body) != "hello" || ct2 != "text/plain" || etag2 != etag {
		t.Fatalf("unexpected get response: body=%q ct=%q etag=%q", string(body), ct2, etag2)
	}

	if deleteErr := st.DeleteObject(ctx, "k1"); deleteErr != nil {
		t.Fatalf("DeleteObject: %v", deleteErr)
	}

	_, _, _, err = st.GetObject(ctx, "k1", 1)
	var nsk *s3types.NoSuchKey
	if err == nil || !errors.As(err, &nsk) {
		t.Fatalf("expected NoSuchKey error, got %v", err)
	}
}

func TestStore_GetObject_EnforcesMaxBytes(t *testing.T) {
	ctx := context.Background()
	st, cleanup := newTestStore(t, "bucket")
	defer cleanup()

	if err := st.PutObject(ctx, "big", []byte("abcd"), "text/plain", ""); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	if _, _, _, err := st.GetObject(ctx, "big", 3); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("expected too large error, got %v", err)
	}
}
