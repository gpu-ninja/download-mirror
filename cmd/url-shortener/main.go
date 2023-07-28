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
	"crypto/tls"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	zaplogfmt "github.com/jsternberg/zap-logfmt"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

func main() {
	project := flag.String("project", "", "GitHub project path (eg. gpu-ninja/koopt)")
	domain := flag.String("domain", "", "Public domain")
	email := flag.String("email", "", "Email address for Let's Encrypt")

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

	if *email == "" {
		logger.Fatal("Let's Encrypt email address is required")
	}

	e := echo.New()
	e.Use(middleware.Recover())

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

	autoTLSManager := autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache("/var/www/.cache"),
		HostPolicy: autocert.HostWhitelist(*domain),
		Email:      *email,
	}

	{
		e := echo.New()
		e.Use(middleware.Recover())
		e.Pre(middleware.HTTPSRedirect())

		// Serve the ACME challenge over HTTP.
		go func() {
			if err := http.ListenAndServe(":8080", autoTLSManager.HTTPHandler(e)); err != nil {
				logger.Fatal("Failed to start ACME server", zap.Error(err))
			}
		}()
	}

	s := http.Server{
		Addr:    ":8443",
		Handler: e,
		TLSConfig: &tls.Config{
			ServerName:     *domain,
			GetCertificate: autoTLSManager.GetCertificate,
			NextProtos:     []string{acme.ALPNProto},
		},
	}

	logger.Info("Listening for connections", zap.String("domain", *domain))

	if err := s.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
		logger.Fatal("Failed to start server", zap.Error(err))
	}
}
