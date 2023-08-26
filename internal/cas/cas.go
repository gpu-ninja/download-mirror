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
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/akamensky/base58"
	"github.com/gpu-ninja/download-mirror/internal/upstream"
	"github.com/labstack/echo/v4"
	"github.com/rogpeppe/go-internal/cache"
	"go.uber.org/zap"
)

// Storage is a cached content addressable storage handler.
type Storage struct {
	logger     *zap.Logger
	baseURL    string
	localCache *cache.Cache
	ups        upstream.Upstream
}

func NewStorage(logger *zap.Logger, baseURL, cacheDir string, ups upstream.Upstream) (*Storage, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create cache directory: %w", err)
	}

	localCache, err := cache.Open(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("failed to open cache: %w", err)
	}

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
	if err != nil || len(id) != cache.HashSize {
		s.logger.Warn("Invalid id", zap.String("id", encodedID), zap.Error(err))

		return echo.NewHTTPError(http.StatusBadRequest)
	}

	file, _, err := s.localCache.GetFile(cache.ActionID(id))
	if err != nil {
		if !strings.Contains(err.Error(), "not found") {
			s.logger.Error("Failed to get file from cache",
				zap.String("id", encodedID), zap.Error(err))

			return echo.NewHTTPError(http.StatusInternalServerError)
		}

		s.logger.Info("Blob not found in local cache", zap.String("id", encodedID))

		r, err := s.ups.Get(cache.ActionID(id))
		if err != nil {
			s.logger.Error("Failed to download blob from upstream", zap.Error(err))

			return echo.NewHTTPError(http.StatusInternalServerError)
		}
		defer r.Close()

		f, err := os.CreateTemp("", "cas-")
		if err != nil {
			s.logger.Error("Failed to create temporary blob file", zap.Error(err))

			return echo.NewHTTPError(http.StatusInternalServerError)
		}
		defer func() {
			_ = f.Close()
			_ = os.Remove(f.Name())
		}()

		if err = c.Stream(http.StatusOK, echo.MIMEOctetStream, io.TeeReader(r, f)); err != nil {
			return err
		}

		if err := f.Sync(); err != nil {
			s.logger.Error("Failed to sync temporary blob file", zap.Error(err))

			return echo.NewHTTPError(http.StatusInternalServerError)
		}

		if _, err := f.Seek(0, io.SeekStart); err != nil {
			s.logger.Error("Failed to rewind temporary blob file", zap.Error(err))

			return echo.NewHTTPError(http.StatusInternalServerError)
		}

		if _, _, err := s.localCache.Put(cache.ActionID(id), f); err != nil {
			s.logger.Error("Failed to store blob in cache", zap.Error(err))

			return echo.NewHTTPError(http.StatusInternalServerError)
		}

		return nil
	}

	s.logger.Info("Blob found in local cache", zap.String("id", encodedID))

	return c.File(file)
}

func (s *Storage) Put(c echo.Context) error {
	body, err := c.FormFile("file")
	if err != nil {
		s.logger.Warn("Failed to get file from form", zap.Error(err))

		return echo.NewHTTPError(http.StatusBadRequest)
	}

	r, err := body.Open()
	if err != nil {
		s.logger.Warn("Failed to open form file", zap.Error(err))

		return echo.NewHTTPError(http.StatusInternalServerError)
	}
	defer r.Close()

	f, err := os.CreateTemp("", "cas-")
	if err != nil {
		s.logger.Error("Failed to create temporary blob file", zap.Error(err))

		return echo.NewHTTPError(http.StatusInternalServerError)
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(f.Name())
	}()

	h := cache.NewHash("")
	if _, err := io.Copy(f, io.TeeReader(r, h)); err != nil {
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

	id := h.Sum()
	encodedID := base58.Encode(id[:])

	s.logger.Info("Received blob", zap.String("id", encodedID))

	_, _, err = s.localCache.Put(cache.ActionID(id), f)
	if err != nil {
		s.logger.Error("Failed to store blob in cache", zap.Error(err))

		return echo.NewHTTPError(http.StatusInternalServerError)
	}

	file, _, err := s.localCache.GetFile(cache.ActionID(id))
	if err != nil {
		s.logger.Error("Failed to get blob from cache", zap.Error(err))

		return echo.NewHTTPError(http.StatusInternalServerError)
	}

	r, err = os.Open(file)
	if err != nil {
		s.logger.Error("Failed to open cached blob file", zap.Error(err))

		return echo.NewHTTPError(http.StatusInternalServerError)
	}
	defer r.Close()

	if err := s.ups.Put(id, r); err != nil {
		s.logger.Error("Failed to upload blob to upstream", zap.Error(err))

		return echo.NewHTTPError(http.StatusInternalServerError)
	}

	return c.String(http.StatusCreated, s.baseURL+"/"+encodedID)
}
