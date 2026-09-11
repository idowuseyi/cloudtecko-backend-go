// Package mock provides an in-memory storage.Storage for tests and for local
// development without object-store credentials.
package mock

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/idowuseyi/cloudtecko-backend-go/internal/storage"
)

// Storage keeps objects in memory.
//
// It is deliberately faithful about the things the media pipeline depends on:
// PresignPut does NOT create an object (so "the client never uploaded" is
// reachable in tests), Head reports the size and type of what was actually
// stored rather than what was declared, and Move is copy-then-delete.
type Storage struct {
	mutex         sync.RWMutex
	objects       map[string]object
	publicBaseURL string

	// FailMoveAfterCopy simulates the non-atomic half of Move: the destination
	// is written and the source is left in place, then an error is returned.
	FailMoveAfterCopy bool
}

type object struct {
	data        []byte
	contentType string
}

func New(publicBaseURL string) *Storage {
	return &Storage{
		objects:       map[string]object{},
		publicBaseURL: strings.TrimSuffix(publicBaseURL, "/"),
	}
}

// PutForTest places an object directly, standing in for the client's PUT to the
// presigned URL.
func (s *Storage) PutForTest(key string, data []byte, contentType string) {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.objects[key] = object{data: data, contentType: contentType}
}

// Keys returns the stored keys, for assertions about promotion and cleanup.
func (s *Storage) Keys() []string {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	keys := make([]string, 0, len(s.objects))
	for key := range s.objects {
		keys = append(keys, key)
	}

	return keys
}

func (s *Storage) PresignPut(_ context.Context, key string, ttl time.Duration) (string, error) {
	// No object is created: a presigned URL is permission to upload, not an
	// upload. Tests rely on this to exercise "presigned but never sent".
	return s.publicBaseURL + "/upload/" + key + "?ttl=" + ttl.String(), nil
}

func (s *Storage) Head(_ context.Context, key string) (storage.ObjectInfo, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	obj, exists := s.objects[key]
	if !exists {
		return storage.ObjectInfo{}, storage.ErrObjectNotFound
	}

	return storage.ObjectInfo{
		Key:         key,
		Size:        int64(len(obj.data)),
		ContentType: obj.contentType,
		ETag:        "mock-etag",
	}, nil
}

func (s *Storage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	obj, exists := s.objects[key]
	if !exists {
		return nil, storage.ErrObjectNotFound
	}

	return io.NopCloser(bytes.NewReader(obj.data)), nil
}

func (s *Storage) Move(_ context.Context, from string, to string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	obj, exists := s.objects[from]
	if !exists {
		return storage.ErrObjectNotFound
	}

	s.objects[to] = obj

	if s.FailMoveAfterCopy {
		return storage.ErrMoveIncomplete
	}

	delete(s.objects, from)

	return nil
}

func (s *Storage) Delete(_ context.Context, key string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	delete(s.objects, key)

	return nil
}

func (s *Storage) PublicURL(key string) string {
	return s.publicBaseURL + "/" + strings.TrimPrefix(key, "/")
}
