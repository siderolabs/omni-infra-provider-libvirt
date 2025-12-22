// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.

package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/siderolabs/omni/client/pkg/constants"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

const (
	// DefaultCachePath is the default directory for caching downloaded images.
	DefaultCachePath = "/tmp/omni-libvirt-cache"

	// DefaultCleanupInterval is the default interval between cleanup runs.
	DefaultCleanupInterval = time.Hour

	// DefaultMaxAge is the default maximum age for unused cached images.
	DefaultMaxAge = time.Hour

	// timeout is the HTTP client timeout for downloading images.
	timeout = time.Second * 120
)

// ImageCache manages downloading and caching of Talos images.
// It deduplicates concurrent downloads using singleflight and provides
// reference counting to safely manage cache cleanup.
type ImageCache struct {
	downloadGroup singleflight.Group
	// Reference counter for images
	refs map[string]int
	// Reset whenever Acquire() is called on a given image key
	lastUsed  map[string]time.Time
	logger    *zap.Logger
	CachePath string
	// How often to run the cleanup job
	CleanupInterval time.Duration
	// Maximum age for locally cached images before they get cleaned up.
	// Takes effect only if the related refCount is zero.
	MaxAge time.Duration
	mu     sync.Mutex
}

// NewImageCache creates a new ImageCache with default settings.
func NewImageCache(logger *zap.Logger, imageCachePath string) *ImageCache {
	return &ImageCache{
		CachePath:       imageCachePath,
		CleanupInterval: DefaultCleanupInterval,
		MaxAge:          DefaultMaxAge,
		refs:            make(map[string]int),
		lastUsed:        make(map[string]time.Time),
		logger:          logger,
	}
}

// cacheKey generates a unique cache key for an image.
func cacheKey(schematicID, talosVersion string) string {
	return fmt.Sprintf("%s-%s.qcow2.gz", schematicID, talosVersion)
}

// Acquire increments the reference count for an image and downloads it, if necessary.
// Returns the path to the cached image file.
// The caller must call Release() when done with the image.
func (c *ImageCache) Acquire(ctx context.Context, schematicID, talosVersion string) (string, error) {
	key := cacheKey(schematicID, talosVersion)
	filePath := filepath.Join(c.CachePath, key)

	// Increment reference count
	c.mu.Lock()
	c.refs[key]++
	c.mu.Unlock()

	// Use singleflight to deduplicate concurrent downloads
	_, err, _ := c.downloadGroup.Do(key, func() (any, error) {
		// Check if already cached
		if _, statErr := os.Stat(filePath); statErr == nil {
			c.logger.Info(
				"image already cached",
				zap.String("key", key),
				zap.String("filePath", filePath),
			)

			return "", nil
		}

		// Download the image
		err := c.download(ctx, key, schematicID, talosVersion)

		return nil, err
	})
	if err != nil {
		// Decrement reference count on error
		c.mu.Lock()

		c.refs[key]--
		if c.refs[key] <= 0 {
			delete(c.refs, key)
		}

		c.mu.Unlock()

		return "", err
	}

	return filePath, nil
}

// Release decrements the reference count for an image and updates the last used time.
func (c *ImageCache) Release(schematicID, talosVersion string) {
	key := cacheKey(schematicID, talosVersion)

	c.mu.Lock()
	defer c.mu.Unlock()

	c.refs[key]--
	if c.refs[key] <= 0 {
		delete(c.refs, key)
		c.lastUsed[key] = time.Now()
	}
}

// Run starts the background cleanup goroutine.
// It should be run in an errgroup alongside other components.
func (c *ImageCache) Run(ctx context.Context) error {
	ticker := time.NewTicker(c.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			c.cleanup()
		}
	}
}

// cleanup removes cached images that are no longer in use and have exceeded MaxAge.
func (c *ImageCache) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()

	entries, err := os.ReadDir(c.CachePath)
	if err != nil {
		c.logger.Warn("failed to read cache directory", zap.Error(err))

		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		key := entry.Name()
		filePath := filepath.Join(c.CachePath, key)

		// Skip if still in use
		if c.refs[key] > 0 {
			c.logger.Info(
				"cached image still in use, skip cleanup",
				zap.String("key", key),
				zap.String("filepath", filePath),
			)

			continue
		}

		// Check if old enough to remove
		lastUsed, ok := c.lastUsed[key]
		if !ok {
			// File exists but we don't have metadata for it
			// This can happen if the process was restarted
			// Set lastUsed to now and wait for next cleanup
			c.lastUsed[key] = now

			continue
		}

		if now.Sub(lastUsed) < c.MaxAge {
			continue
		}

		// Remove the file
		if err := os.Remove(filePath); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				c.logger.Warn("failed to remove cached image",
					zap.String("file", filePath),
					zap.Error(err))
			}

			continue
		}

		delete(c.lastUsed, key)
		c.logger.Info(
			"removed cached image",
			zap.String("key", key),
			zap.String("filepath", filePath),
		)
	}
}

// download fetches an image from the image factory and saves it to the cache.
// It uses a temporary file and atomic rename to prevent partial downloads.
func (c *ImageCache) download(ctx context.Context, key, schematicID, talosVersion string) error {
	imageURL, err := url.Parse(constants.ImageFactoryBaseURL)
	if err != nil {
		return fmt.Errorf("failed to parse image factory URL: %w", err)
	}

	imageURL = imageURL.JoinPath(
		"image",
		schematicID,
		talosVersion,
		"metal-amd64.qcow2.gz",
	)

	c.logger.Info("downloading image",
		zap.String("schematic_id", schematicID),
		zap.String("talos_version", talosVersion),
		zap.String("url", imageURL.String()),
	)

	// Use context.WithoutCancel to ensure we complete the download
	// even if the parent context is canceled
	reqCtx := context.WithoutCancel(ctx)

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, imageURL.String(), nil)
	if err != nil {
		return fmt.Errorf("error creating request: %w", err)
	}

	client := http.Client{
		Timeout: timeout,
	}

	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("error fetching image: %w", err)
	}
	defer res.Body.Close() //nolint:errcheck

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", res.StatusCode)
	}

	// Create temp file in the same directory to ensure atomic rename works
	tempFile, err := os.CreateTemp(c.CachePath, "download-*.tmp")
	if err != nil {
		return fmt.Errorf("error creating temp file: %w", err)
	}

	tempPath := tempFile.Name()

	// Clean up temp file on error
	defer func() {
		if tempPath != "" {
			// if an error happens here, we log, but don't care about it furthers
			if errRemove := os.Remove(tempPath); errRemove != nil {
				c.logger.Debug(
					"error removing temp file",
					zap.String("tempPath", tempPath),
					zap.Error(errRemove),
				)
			}
		}
	}()

	// Download to temp file
	_, err = io.Copy(tempFile, res.Body)
	if err != nil {
		tempFile.Close() //nolint:errcheck

		return fmt.Errorf("error downloading image: %w", err)
	}

	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("error closing temp file: %w", err)
	}

	// Atomic rename to final location
	finalPath := filepath.Join(c.CachePath, key)

	if err := os.Rename(tempPath, finalPath); err != nil {
		return fmt.Errorf("error moving image to cache: %w", err)
	}

	// Clear tempPath so defer doesn't try to remove it
	tempPath = ""

	c.logger.Info(
		"downloaded image",
		zap.String("key", key),
		zap.String("filepath", finalPath),
	)

	return nil
}
