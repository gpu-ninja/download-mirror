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

package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	zaplogfmt "github.com/jsternberg/zap-logfmt"
	"github.com/labstack/echo/v4"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/crypto/acme/autocert"
)

func main() {
	listen := flag.String("listen", ":8443", "Listen address")
	project := flag.String("project", "", "GitHub project path (eg. gpu-ninja/koopt)")
	domain := flag.String("domain", "", "Public domain")
	letsEnceyptEmail := flag.String("email", "", "Email address for Let's Encrypt")

	flag.Parse()

	config := zap.NewProductionEncoderConfig()
	config.EncodeTime = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendInt64(t.Unix())
	}

	logger := zap.New(zapcore.NewCore(
		zaplogfmt.NewEncoder(config),
		os.Stdout,
		zapcore.InfoLevel,
	))
	defer func() {
		_ = logger.Sync()
	}()

	if *project == "" {
		logger.Fatal("Github project path is required")
	}

	if *domain == "" {
		logger.Fatal("Public hostname is required")
	}

	if *letsEnceyptEmail == "" {
		logger.Fatal("Let's Encrypt email address is required")
	}

	e := echo.New()
	e.AutoTLSManager.HostPolicy = autocert.HostWhitelist(*domain)
	e.AutoTLSManager.Email = *letsEnceyptEmail
	e.AutoTLSManager.Prompt = autocert.AcceptTOS
	e.AutoTLSManager.Cache = autocert.DirCache("/var/www/.cache")

	e.GET("/latest/:assetPath", func(c echo.Context) error {
		assetPath := c.Param("assetPath")

		// Make sure the asset path is a valid path and doesn't contain any
		// directory traversal or other invalid characters.
		sanitizedAssetPath := url.PathEscape(assetPath)
		if strings.Contains(assetPath, "/") || strings.Contains(assetPath, "..") || assetPath != sanitizedAssetPath {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid path")
		}

		redirectURL := fmt.Sprintf("https://github.com/%s/releases/latest/download/%s", *project, sanitizedAssetPath)

		logger.Info("Handling request", zap.String("redirectURL", redirectURL))

		return c.Redirect(http.StatusFound, redirectURL)
	})

	logger.Info("Starting server", zap.String("port", ":443"))

	if err := e.StartAutoTLS(*listen); err != nil {
		logger.Fatal("Failed to start server", zap.Error(err))
	}
}
