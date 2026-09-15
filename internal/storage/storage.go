// Package storage abstracts the object store behind the media pipeline.
package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrObjectNotFound is returned by Head and Get when the key does not exist.
// Callers distinguish it from a transport failure: a missing object after a
// presigned upload means the client never actually sent the bytes, which is a
// 400, whereas a transport failure is a 500.
var (
	ErrObjectNotFound = errors.New("storage: object not found")

	// ErrMoveIncomplete reports that a Move copied the object but could not
	// remove the source. The destination IS written, so the caller must not
	// retry blindly as if nothing happened.
	ErrMoveIncomplete = errors.New("storage: object copied but source not removed")
)

// ObjectInfo is what the store reports about a stored object.
//
// Size and ContentType here are the STORE's view, read back after the upload.
// They are the only trustworthy values: a presigned PUT hands the client a URL
// and the client chooses the bytes, so anything the client declared beforehand
// is a claim, not a fact.
type ObjectInfo struct {
	Key         string
	Size        int64
	ContentType string
	ETag        string
}

// Storage is the object-store seam. The S3 implementation targets any
// S3-compatible endpoint (Cloudflare R2 in production); the mock keeps the
// media pipeline testable without network or credentials.
type Storage interface {
	// PresignPut returns a URL the client may PUT to, valid for ttl.
	//
	// There is deliberately NO contentType parameter. An S3 presigned PUT signs
	// the method, bucket, key and expiry -- it cannot bind Content-Type, and no
	// S3-compatible store can constrain the bytes the client actually sends.
	// Accepting a contentType here would promise an enforcement that does not
	// exist and invite callers to skip the checks that do. Verification happens
	// after the upload, via Head and a content sniff.
	PresignPut(ctx context.Context, key string, ttl time.Duration) (string, error)

	// Head returns the store's own view of an object.
	Head(ctx context.Context, key string) (ObjectInfo, error)

	// Get opens the object for reading. Callers that only need the leading bytes
	// for a content sniff should wrap the reader in an io.LimitReader rather than
	// pulling the whole object.
	Get(ctx context.Context, key string) (io.ReadCloser, error)

	// Move relocates an object, typically quarantine -> public, REWRITING its
	// content type to contentType.
	//
	// The rewrite is the security-relevant half. A presigned PUT signs only the
	// method, bucket, key and expiry, so the uploading client chooses the
	// Content-Type the store records and there is no signature binding it to
	// anything we approved. Copying the object as-is would carry that
	// client-chosen header onto the public object: bytes that sniff as an
	// allow-listed image, served to a browser as whatever the uploader asked
	// for. Promotion therefore replaces it with the type we sniffed ourselves.
	//
	// On S3-compatible stores this is COPY followed by DELETE, not an atomic
	// rename. A failure between the two leaves the source in place and the
	// destination written; implementations must therefore be safe to retry, and
	// callers must treat a Move error as "state unknown" rather than "nothing
	// happened".
	Move(ctx context.Context, from string, to string, contentType string) error

	Delete(ctx context.Context, key string) error

	// PublicURL is the address the object is served from once public. It is a
	// pure function of the key -- no network call -- so it is safe to compute
	// while holding a database transaction.
	PublicURL(key string) string
}
