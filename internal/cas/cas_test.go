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

package cas_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/akamensky/base58"
	"github.com/gpu-ninja/download-mirror/internal/cas"
	"github.com/gpu-ninja/download-mirror/internal/securehash"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestContentAddressableStorage(t *testing.T) {
	ctx := context.Background()

	logger := zaptest.NewLogger(t)

	hashBuilder := securehash.NewBuilder().
		WithSecret([]byte("test"))

	ups := &fsUpstream{
		dir: t.TempDir(),
	}

	cacheCtx, cancel := context.WithCancel(ctx)

	s, err := cas.NewStorage(cacheCtx, logger, t.TempDir(), 0, hashBuilder, "https://example.com/blobs", ups)
	require.NoError(t, err)

	data := make([]byte, 1000000)
	_, err = io.ReadFull(rand.Reader, data)
	require.NoError(t, err)

	e := echo.New()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "test.bin")
	require.NoError(t, err)

	_, err = io.Copy(part, bytes.NewReader(data))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/", &body)
	req.Header.Set(echo.HeaderContentType, writer.FormDataContentType())
	rec := httptest.NewRecorder()

	err = s.Put(e.NewContext(req, rec))
	require.NoError(t, err)

	assert.Equal(t, http.StatusCreated, rec.Code)

	blobURL := rec.Body.String()
	assert.NotEmpty(t, blobURL)

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	rec = httptest.NewRecorder()

	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(strings.Split(blobURL, "/")[4])

	err = s.Get(c)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, data, rec.Body.Bytes())

	cancel()
	cacheCtx, cancel = context.WithCancel(ctx)
	defer cancel()

	// New empty cache directory.
	s, err = cas.NewStorage(cacheCtx, logger, t.TempDir(), 0, hashBuilder, "https://example.com/blobs", ups)
	require.NoError(t, err)

	req = httptest.NewRequest(http.MethodGet, "/", nil)
	rec = httptest.NewRecorder()

	c = e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues(strings.Split(blobURL, "/")[4])

	err = s.Get(c)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, data, rec.Body.Bytes())
}

type fsUpstream struct {
	dir string
}

func (ups *fsUpstream) Get(id [securehash.Size]byte) (io.ReadCloser, error) {
	return os.Open(filepath.Join(ups.dir, base58.Encode(id[:])))
}

func (ups *fsUpstream) Put(id [securehash.Size]byte, r io.Reader) error {
	f, err := os.Create(filepath.Join(ups.dir, base58.Encode(id[:])))
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, r)
	return err
}
