package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/awomore/Pill4rsBE/internal/db"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// Media kinds and global size caps. Images are capped at 30 MB; videos at 4 GB
// (500 MB is the recommended API-upload ceiling, enforced per platform later).
const (
	MediaKindImage = "image"
	MediaKindVideo = "video"

	MaxImageBytes = 30 << 20
	MaxVideoBytes = 4 << 30

	// MaxAPIUploadBytes caps what the JSON/multipart API accepts in one request.
	// Larger video should use a direct-to-storage signed upload (S3/R2).
	MaxAPIUploadBytes = 64 << 20
)

// Storage abstracts object storage. LocalStorage ships for development; an
// S3/R2 adapter implements the same interface for production (global CDN).
type Storage interface {
	Put(ctx context.Context, key string, data []byte, contentType string) (publicURL string, err error)
	Delete(ctx context.Context, key string) error
}

// LocalStorage writes media to disk and serves it under /media/<key>.
type LocalStorage struct {
	dir        string
	publicBase string
}

func NewLocalStorage(dir, publicBase string) *LocalStorage {
	return &LocalStorage{dir: dir, publicBase: strings.TrimRight(publicBase, "/")}
}

func (l *LocalStorage) Put(_ context.Context, key string, data []byte, _ string) (string, error) {
	full := filepath.Join(l.dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", fmt.Errorf("create media dir: %w", err)
	}
	if err := os.WriteFile(full, data, 0o644); err != nil {
		return "", fmt.Errorf("write media: %w", err)
	}
	return l.publicBase + "/media/" + key, nil
}

func (l *LocalStorage) Delete(_ context.Context, key string) error {
	full := filepath.Join(l.dir, filepath.FromSlash(key))
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// MediaService stores uploaded creative assets and records their metadata.
type MediaService struct {
	queries *db.Queries
	storage Storage
}

func NewMediaService(queries *db.Queries, storage Storage) *MediaService {
	return &MediaService{queries: queries, storage: storage}
}

// Upload validates, stores, and records one image or video asset.
func (s *MediaService) Upload(ctx context.Context, workspaceID uuid.UUID, filename, contentType string, data []byte) (db.MediaAsset, error) {
	contentType = strings.TrimSpace(strings.Split(contentType, ";")[0])
	if contentType == "" || contentType == "application/octet-stream" {
		contentType = http.DetectContentType(data)
	}

	kind, err := mediaKind(contentType, filename)
	if err != nil {
		return db.MediaAsset{}, err
	}

	limit := int64(MaxImageBytes)
	if kind == MediaKindVideo {
		limit = MaxAPIUploadBytes
		if MaxVideoBytes < limit {
			limit = MaxVideoBytes
		}
	}
	if int64(len(data)) > limit {
		return db.MediaAsset{}, ValidationError{fmt.Sprintf("%s exceeds the %d MB limit", kind, limit>>20)}
	}

	ext := strings.ToLower(filepath.Ext(filename))
	if ext == "" {
		ext = defaultExt(contentType)
	}
	key := path.Join(workspaceID.String(), uuid.NewString()+ext)

	publicURL, err := s.storage.Put(ctx, key, data, contentType)
	if err != nil {
		return db.MediaAsset{}, err
	}

	sum := sha256.Sum256(data)
	var width, height pgtype.Int4
	if kind == MediaKindImage {
		if cfg, _, derr := image.DecodeConfig(bytes.NewReader(data)); derr == nil {
			width = pgtype.Int4{Int32: int32(cfg.Width), Valid: true}
			height = pgtype.Int4{Int32: int32(cfg.Height), Valid: true}
		}
	}

	return s.queries.CreateMediaAsset(ctx, db.CreateMediaAssetParams{
		WorkspaceID: pgUUID(workspaceID),
		Kind:        kind,
		StorageKey:  key,
		PublicUrl:   publicURL,
		Mime:        contentType,
		Bytes:       int64(len(data)),
		Width:       width,
		Height:      height,
		Checksum:    pgtype.Text{String: hex.EncodeToString(sum[:]), Valid: true},
		Status:      "ready",
	})
}

// List returns the workspace's media, newest first.
func (s *MediaService) List(ctx context.Context, workspaceID uuid.UUID) ([]db.MediaAsset, error) {
	return s.queries.ListMediaAssets(ctx, pgUUID(workspaceID))
}

// Get returns one asset scoped to the workspace.
func (s *MediaService) Get(ctx context.Context, workspaceID, assetID uuid.UUID) (db.MediaAsset, error) {
	return s.queries.GetMediaAsset(ctx, db.GetMediaAssetParams{
		ID:          pgUUID(assetID),
		WorkspaceID: pgUUID(workspaceID),
	})
}

// Delete removes the asset from storage and the database.
func (s *MediaService) Delete(ctx context.Context, workspaceID, assetID uuid.UUID) error {
	asset, err := s.Get(ctx, workspaceID, assetID)
	if err != nil {
		return err
	}
	if err := s.storage.Delete(ctx, asset.StorageKey); err != nil {
		return err
	}
	return s.queries.DeleteMediaAsset(ctx, db.DeleteMediaAssetParams{
		ID:          pgUUID(assetID),
		WorkspaceID: pgUUID(workspaceID),
	})
}

func mediaKind(contentType, filename string) (string, error) {
	switch {
	case strings.HasPrefix(contentType, "image/"):
		return MediaKindImage, nil
	case strings.HasPrefix(contentType, "video/"):
		return MediaKindVideo, nil
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return MediaKindImage, nil
	case ".mp4", ".mov", ".m4v", ".webm":
		return MediaKindVideo, nil
	}
	return "", ValidationError{"only image and video files are supported"}
}

func defaultExt(contentType string) string {
	switch contentType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "video/webm":
		return ".webm"
	default:
		return ""
	}
}
