package local

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"

	"github.com/aarondl/null/v8"
	"github.com/aarondl/sqlboiler/v4/boil"
	"github.com/gabriel-vasile/mimetype"
	"github.com/google/uuid"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/api/httperrors"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/data/dto"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/models"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/storage"
	"github.com/idowuseyi/cloudtecko-backend-go/internal/util"
)

// sniffLimit is how many leading bytes are read to determine the real content
// type. mimetype needs at most ~3KB for the formats in the allow-list; reading
// the whole object would let a large upload tie up a request.
const sniffLimit = 3072

// PresignMediaUpload records an intended upload and returns a URL the client may
// PUT to.
//
// The object lands under the quarantine prefix. Nothing is trusted yet: a
// presigned PUT is permission to write bytes, and the client chooses which
// bytes, so contentType and byteSize here are the client's CLAIMS, stored only
// so the completion step can report what was promised versus what arrived.
func (s *Service) PresignMediaUpload(ctx context.Context, request dto.MediaPresignRequest) (dto.MediaPresign, error) {
	log := util.LogFromContext(ctx)

	if request.ByteSize > s.config.Storage.MaxUploadBytes {
		return dto.MediaPresign{}, httperrors.ErrBadRequestUploadTooLarge
	}

	// Reject an implausible declared type early. This is a courtesy check that
	// saves a pointless round trip; it is NOT the control. The real check runs
	// against the sniffed type at completion.
	if !slices.Contains(s.config.Storage.AllowedContentTypes, request.ContentType) {
		return dto.MediaPresign{}, httperrors.ErrBadRequestUnsupportedMediaType
	}

	objectKey := path.Join(s.config.Storage.QuarantinePrefix, uuid.NewString(), safeFilename(request.Filename))

	asset := &models.MediaAsset{
		ObjectKey:        objectKey,
		Bucket:           s.config.Storage.Bucket,
		ContentType:      request.ContentType,
		ByteSize:         request.ByteSize,
		Status:           models.MediaStatusPending,
		UploadedByUserID: null.StringFrom(request.UploadedByUserID),
	}

	if err := asset.Insert(ctx, s.db, boil.Infer()); err != nil {
		log.Err(err).Msg("Failed to insert media asset")

		return dto.MediaPresign{}, fmt.Errorf("failed to insert media asset: %w", err)
	}

	uploadURL, err := s.storage.PresignPut(ctx, objectKey, s.config.Storage.PresignTTL)
	if err != nil {
		log.Err(err).Msg("Failed to presign upload")

		return dto.MediaPresign{}, fmt.Errorf("failed to presign upload: %w", err)
	}

	return dto.MediaPresign{
		ID:        asset.ID,
		UploadURL: uploadURL,
		ObjectKey: objectKey,
		ExpiresAt: s.clock.Now().Add(s.config.Storage.PresignTTL),
	}, nil
}

// CompleteMediaUpload verifies an uploaded object and promotes it.
//
// This is where the real validation happens, and it must, because a presigned
// PUT can constrain nothing about the payload:
//
//  1. HEAD the object -- proves it exists and gives the TRUE stored size.
//  2. Sniff the leading bytes -- gives the TRUE content type, regardless of what
//     the client declared or what extension it used.
//  3. Only then promote quarantine -> public and mark it ready.
//
// A rejected object is deleted rather than left in quarantine, so a caller
// cannot use failed completions as free storage.
func (s *Service) CompleteMediaUpload(ctx context.Context, assetID string) (dto.Media, error) {
	log := util.LogFromContext(ctx)

	asset, err := models.FindMediaAsset(ctx, s.db, assetID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return dto.Media{}, httperrors.ErrNotFoundMedia
		}

		return dto.Media{}, fmt.Errorf("failed to load media asset: %w", err)
	}

	// Completing twice is not an error: the second call reports the asset as it
	// already stands. Clients retry, and a retry must not destroy a live asset.
	if asset.Status == models.MediaStatusReady {
		return mediaToDTO(asset), nil
	}

	info, err := s.storage.Head(ctx, asset.ObjectKey)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotFound) {
			// Presigned but never actually uploaded.
			return dto.Media{}, httperrors.ErrBadRequestUploadMissing
		}

		log.Err(err).Msg("Failed to head uploaded object")

		return dto.Media{}, fmt.Errorf("failed to head uploaded object: %w", err)
	}

	if info.Size > s.config.Storage.MaxUploadBytes {
		return dto.Media{}, s.rejectMedia(ctx, asset, httperrors.ErrBadRequestUploadTooLarge)
	}

	if info.Size == 0 {
		return dto.Media{}, s.rejectMedia(ctx, asset, httperrors.ErrBadRequestUploadMissing)
	}

	detected, err := s.sniffContentType(ctx, asset.ObjectKey)
	if err != nil {
		log.Err(err).Msg("Failed to sniff uploaded object")

		return dto.Media{}, fmt.Errorf("failed to sniff uploaded object: %w", err)
	}

	if !slices.Contains(s.config.Storage.AllowedContentTypes, detected) {
		log.Debug().
			Str("declared", asset.ContentType).
			Str("detected", detected).
			Msg("Rejecting upload whose real content type is not allowed")

		return dto.Media{}, s.rejectMedia(ctx, asset, httperrors.ErrBadRequestUnsupportedMediaType)
	}

	publicKey := path.Join(s.config.Storage.PublicPrefix, strings.TrimPrefix(asset.ObjectKey, s.config.Storage.QuarantinePrefix+"/"))

	if err := s.storage.Move(ctx, asset.ObjectKey, publicKey); err != nil {
		// Move is copy-then-delete and is not atomic. On failure the destination
		// may exist; the asset stays 'pending' so the orphan sweep can reconcile
		// it, and the caller is told the outcome is unknown rather than "nothing
		// happened".
		log.Err(err).Str("from", asset.ObjectKey).Str("to", publicKey).Msg("Failed to promote uploaded object")

		return dto.Media{}, fmt.Errorf("failed to promote uploaded object: %w", err)
	}

	asset.ObjectKey = publicKey
	asset.ContentType = detected
	asset.ByteSize = info.Size
	asset.Status = models.MediaStatusReady
	asset.ChecksumSha256 = null.NewString(info.ETag, info.ETag != "")
	asset.PublicURL = null.StringFrom(s.storage.PublicURL(publicKey))

	if _, err := asset.Update(ctx, s.db, boil.Whitelist(
		models.MediaAssetColumns.ObjectKey,
		models.MediaAssetColumns.ContentType,
		models.MediaAssetColumns.ByteSize,
		models.MediaAssetColumns.Status,
		models.MediaAssetColumns.ChecksumSha256,
		models.MediaAssetColumns.PublicURL,
		models.MediaAssetColumns.UpdatedAt,
	)); err != nil {
		log.Err(err).Msg("Failed to mark media asset ready")

		return dto.Media{}, fmt.Errorf("failed to mark media asset ready: %w", err)
	}

	return mediaToDTO(asset), nil
}

// rejectMedia marks an asset rejected and removes the offending object, then
// returns the caller-facing error. Storage failures during cleanup are logged
// but do not mask the original rejection -- the client still needs to know WHY
// its upload was refused.
func (s *Service) rejectMedia(ctx context.Context, asset *models.MediaAsset, cause error) error {
	log := util.LogFromContext(ctx)

	if err := s.storage.Delete(ctx, asset.ObjectKey); err != nil {
		log.Err(err).Str("objectKey", asset.ObjectKey).Msg("Failed to delete rejected upload")
	}

	asset.Status = models.MediaStatusRejected
	if _, err := asset.Update(ctx, s.db, boil.Whitelist(
		models.MediaAssetColumns.Status,
		models.MediaAssetColumns.UpdatedAt,
	)); err != nil {
		log.Err(err).Msg("Failed to mark media asset rejected")
	}

	return cause
}

func (s *Service) sniffContentType(ctx context.Context, objectKey string) (string, error) {
	reader, err := s.storage.Get(ctx, objectKey)
	if err != nil {
		return "", fmt.Errorf("failed to open object: %w", err)
	}

	defer func() {
		if err := reader.Close(); err != nil {
			util.LogFromContext(ctx).Debug().Err(err).Msg("Failed to close object reader")
		}
	}()

	detected, err := mimetype.DetectReader(io.LimitReader(reader, sniffLimit))
	if err != nil {
		return "", fmt.Errorf("failed to detect content type: %w", err)
	}

	// mimetype returns e.g. "image/jpeg; charset=utf-8" for some types; compare
	// on the bare media type.
	return strings.TrimSpace(strings.Split(detected.String(), ";")[0]), nil
}

// safeFilename reduces a client-supplied filename to something safe to place in
// an object key. Path traversal is the obvious risk, but so is a filename that
// changes how the object is served -- so only the base name survives, and only
// its safe characters.
func safeFilename(filename string) string {
	base := path.Base(strings.ReplaceAll(filename, "\\", "/"))
	base = strings.TrimSpace(base)

	var builder strings.Builder

	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}

	cleaned := strings.Trim(builder.String(), ".-")
	if cleaned == "" || cleaned == "." || cleaned == ".." {
		return "upload"
	}

	return cleaned
}

func mediaToDTO(asset *models.MediaAsset) dto.Media {
	return dto.Media{
		ID:          asset.ID,
		Status:      asset.Status,
		ContentType: asset.ContentType,
		ByteSize:    asset.ByteSize,
		PublicURL:   asset.PublicURL,
	}
}
