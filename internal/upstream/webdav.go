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

package upstream

import (
	"fmt"
	"io"

	"github.com/akamensky/base58"
	"github.com/rogpeppe/go-internal/cache"
	"github.com/studio-b12/gowebdav"
)

type WebDAV struct {
	client *gowebdav.Client
}

func NewWebDAV(uri, user, password string) (*WebDAV, error) {
	c := gowebdav.NewClient(uri, user, password)
	if err := c.Connect(); err != nil {
		return nil, fmt.Errorf("failed to connect: %w", err)
	}

	return &WebDAV{
		client: c,
	}, nil
}

func (w *WebDAV) Get(id [cache.HashSize]byte) (io.ReadCloser, error) {
	return w.client.ReadStream(base58.Encode(id[:]))
}

func (w *WebDAV) Put(id [cache.HashSize]byte, r io.Reader) error {
	return w.client.WriteStream(base58.Encode(id[:]), r, 0o644)
}
