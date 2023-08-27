/* SPDX-License-Identifier: Apache-2.0
 *
 * Copyright 2023 Damian Peckett <damian@peckett>.
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

package cache_test

import (
	"bytes"
	"crypto/rand"
	"io"
	"os"
	"testing"
	"time"

	"github.com/gpu-ninja/download-mirror/internal/cache"
	"github.com/gpu-ninja/download-mirror/internal/securehash"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

func TestCache(t *testing.T) {
	logger := zaptest.NewLogger(t)

	blob := make([]byte, 1000000)
	_, err := io.ReadFull(rand.Reader, blob)
	require.NoError(t, err)

	cacheDir := t.TempDir()

	hashBuilder := securehash.NewBuilder().
		WithSecret([]byte("test"))

	c, err := cache.Open(logger, cacheDir, hashBuilder, func() time.Time {
		return time.Now().Add(-5 * time.Hour)
	})
	require.NoError(t, err)

	id, size, err := c.Put(bytes.NewReader(blob))
	require.NoError(t, err)

	assert.Equal(t, len(id), securehash.Size)
	assert.Equal(t, int64(len(blob)), size)

	// Should be a no-op.
	_, _, err = c.Put(bytes.NewReader(blob))
	require.NoError(t, err)

	file, e, err := c.GetFile(id)
	require.NoError(t, err)

	assert.NotEmpty(t, file)
	assert.Equal(t, int64(len(blob)), e.Size)
	assert.NotZero(t, e.Time)

	readBlob, err := os.ReadFile(file)
	require.NoError(t, err)

	assert.Equal(t, blob, readBlob)

	totalSize, err := c.Size()
	require.NoError(t, err)

	assert.Equal(t, int64(len(blob)), totalSize)

	err = c.Trim(0)
	require.NoError(t, err)

	_, _, err = c.GetFile(id)
	require.NoError(t, err)

	c, err = cache.Open(logger, cacheDir, hashBuilder, nil)
	require.NoError(t, err)

	err = c.Trim(1000)
	require.NoError(t, err)

	_, _, err = c.GetFile(id)
	require.Error(t, err)
}
