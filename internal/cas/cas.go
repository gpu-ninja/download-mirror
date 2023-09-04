/* SPDX-License-Identifier: Apache-2.0
 *
 * Copyright 2023 Damian Peckett <damian@pecke.tt>.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package cas

import (
	"context"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/akamensky/base58"
	"github.com/gpu-ninja/blobcache"
	"github.com/gpu-ninja/download-mirror/internal/securehash"
	"github.com/gpu-ninja/download-mirror/internal/upstream"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const (
	cacheTrimInterval = 5 * time.Minute
)

// Storage is a cached content addressable storage handler.
type Storage struct {
	logger     *zap.Logger
	baseURL    string
	localCache *blobcache.Cache
	ups        upstream.Upstream
}

func NewStorage(ctx context.Context, logger *zap.Logger, cacheDir string, cacheMaxBytes int64, secureHashSecret []byte, baseURL string, ups upstream.Upstream) (*Storage, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	localCache, err := blobcache.NewCache(logger, cacheDir, func() hash.Hash {
		return securehash.New(secureHashSecret)
	}, securehash.Size, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open cache: %w", err)
	}

	ticker := time.NewTicker(cacheTrimInterval)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				logger.Info("Trimming cache")

				if err := localCache.Trim(cacheMaxBytes); err != nil {
					logger.Error("Failed to trim cache", zap.Error(err))
				}
			}
		}
	}()

	return &Storage{
		logger:     logger,
		baseURL:    baseURL,
		localCache: localCache,
		ups:        ups,
	}, nil
}

func (s *Storage) Get(c echo.Context) error {
	encodedID := c.Param("id")

	s.logger.Info("Received request for blob", zap.String("id", encodedID))

	id, err := base58.Decode(encodedID)
	if err != nil || len(id) != securehash.Size {
		s.logger.Warn("Invalid id", zap.String("id", encodedID), zap.Error(err))

		return echo.NewHTTPError(http.StatusBadRequest)
	}

	cacheReader, entry, err := s.localCache.Get(id)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		s.logger.Error("Failed to get file from cache",
			zap.String("id", encodedID), zap.Error(err))

		return echo.NewHTTPError(http.StatusInternalServerError)
	} else if err == nil {
		defer cacheReader.Close()

		s.logger.Info("Blob found in local cache", zap.String("id", encodedID))

		c.Response().Header().Set(echo.HeaderContentLength, fmt.Sprintf("%d", entry.Size))

		return c.Stream(http.StatusOK, echo.MIMEOctetStream, cacheReader)
	}

	s.logger.Info("Blob not found in local cache", zap.String("id", encodedID))

	r, err := s.ups.Get(id)
	if err != nil {
		s.logger.Error("Failed to download blob from upstream", zap.Error(err))

		if errors.Is(err, upstream.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound)
		}

		return echo.NewHTTPError(http.StatusInternalServerError)
	}
	defer r.Close()

	f, err := os.CreateTemp("", "blob-")
	if err != nil {
		s.logger.Error("Failed to create temporary blob file", zap.Error(err))

		return echo.NewHTTPError(http.StatusInternalServerError)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()

	dataAvailableCh := make(chan struct{}, 1)

	g, ctx := errgroup.WithContext(c.Request().Context())

	g.Go(func() error {
		defer close(dataAvailableCh)

		if _, err = copyContext(ctx, io.MultiWriter(f, writerFunc(func(p []byte) (int, error) {
			dataAvailableCh <- struct{}{}

			return len(p), nil
		})), r); err != nil {
			return fmt.Errorf("failed to read blob from upstream: %w", err)
		}

		return nil
	})

	g.Go(func() error {
		var writtenBytes int64
		buf := make([]byte, 32*1024)

		for range dataAvailableCh {
			if writtenBytes == 0 {
				c.Response().Header().Set(echo.HeaderContentType, echo.MIMEOctetStream)
				c.Response().WriteHeader(http.StatusOK)
			}

			pos, err := f.Seek(0, io.SeekCurrent)
			if err != nil {
				return fmt.Errorf("failed to get current position in temporary blob file: %w", err)
			}

			chunkSize := pos - writtenBytes
			if chunkSize > int64(len(buf)) {
				chunkSize = int64(len(buf))
			}

			nr, err := f.ReadAt(buf[:chunkSize], writtenBytes)
			if err != nil && !errors.Is(err, io.EOF) {
				return fmt.Errorf("failed to read from temporary blob file: %w", err)
			}

			nw, err := c.Response().Write(buf[:nr])
			if err != nil || nw != nr {
				return fmt.Errorf("failed to write to client: %w", err)
			}

			writtenBytes += int64(nw)
		}

		return nil
	})

	if err := g.Wait(); err != nil {
		s.logger.Error("Encountered error transferring blob", zap.Error(err))

		return echo.NewHTTPError(http.StatusInternalServerError)
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		s.logger.Error("Failed to rewind temporary blob file", zap.Error(err))

		return echo.NewHTTPError(http.StatusInternalServerError)
	}

	if _, _, err := s.localCache.Put(f); err != nil {
		s.logger.Error("Failed to store blob in cache", zap.Error(err))

		return echo.NewHTTPError(http.StatusInternalServerError)
	}

	return nil
}

func (s *Storage) Put(c echo.Context) error {
	body, err := c.FormFile("file")
	if err != nil {
		s.logger.Warn("Failed to get file from form", zap.Error(err))

		return echo.NewHTTPError(http.StatusBadRequest)
	}

	s.logger.Info("Received request to store blob",
		zap.String("name", body.Filename))

	r, err := body.Open()
	if err != nil {
		s.logger.Warn("Failed to open form file", zap.Error(err))

		return echo.NewHTTPError(http.StatusInternalServerError)
	}
	defer r.Close()

	f, err := os.CreateTemp("", "blob-")
	if err != nil {
		s.logger.Error("Failed to create temporary blob file", zap.Error(err))

		return echo.NewHTTPError(http.StatusInternalServerError)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()

	if _, err := copyContext(c.Request().Context(), f, r); err != nil {
		s.logger.Warn("Failed to get blob from client", zap.Error(err))

		return echo.NewHTTPError(http.StatusInternalServerError)
	}

	if err := f.Sync(); err != nil {
		s.logger.Error("Failed to sync temporary blob file", zap.Error(err))

		return echo.NewHTTPError(http.StatusInternalServerError)
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		s.logger.Error("Failed to rewind temporary blob file", zap.Error(err))

		return echo.NewHTTPError(http.StatusInternalServerError)
	}

	id, _, err := s.localCache.Put(f)
	if err != nil {
		s.logger.Error("Failed to store blob in cache", zap.Error(err))

		return echo.NewHTTPError(http.StatusInternalServerError)
	}

	encodedID := base58.Encode(id[:])

	s.logger.Info("Received blob", zap.String("name", body.Filename),
		zap.String("id", encodedID))

	cacheReader, _, err := s.localCache.Get(id)
	if err != nil {
		s.logger.Error("Failed to get blob from cache", zap.Error(err))

		return echo.NewHTTPError(http.StatusInternalServerError)
	}
	defer cacheReader.Close()

	if err := s.ups.Put(id, cacheReader); err != nil {
		s.logger.Error("Failed to upload blob to upstream", zap.Error(err))

		return echo.NewHTTPError(http.StatusInternalServerError)
	}

	return c.String(http.StatusCreated, fmt.Sprintf("%s/%s/%s", s.baseURL, encodedID, url.PathEscape(body.Filename)))
}

func copyContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	return io.Copy(dst, readerFunc(func(p []byte) (int, error) {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
			return src.Read(p)
		}
	}))
}

type readerFunc func(p []byte) (n int, err error)

func (f readerFunc) Read(p []byte) (n int, err error) {
	return f(p)
}

type writerFunc func(p []byte) (n int, err error)

func (f writerFunc) Write(p []byte) (n int, err error) {
	return f(p)
}
