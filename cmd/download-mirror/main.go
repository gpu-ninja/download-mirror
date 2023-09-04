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
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/adrg/xdg"
	"github.com/docker/go-units"
	"github.com/gpu-ninja/download-mirror/internal/cas"
	"github.com/gpu-ninja/download-mirror/internal/upstream"
	zaplogfmt "github.com/jsternberg/zap-logfmt"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/urfave/cli/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

func main() {
	config := zap.NewProductionEncoderConfig()
	logger := zap.New(zapcore.NewCore(
		zaplogfmt.NewEncoder(config),
		os.Stdout,
		zapcore.InfoLevel,
	))

	defaultCacheDir, err := xdg.CacheFile("download-mirror")
	if err != nil {
		logger.Fatal("Failed to get default cache directory", zap.Error(err))
	}

	app := &cli.App{
		Name:  "download-mirror",
		Usage: "CDN frontend for Hetzner Storage Boxes.",
		Flags: []cli.Flag{
			&cli.BoolFlag{
				Name:    "dev",
				Usage:   "Development mode",
				EnvVars: []string{"DEV"},
			},
			&cli.StringFlag{
				Name:    "domain",
				Usage:   "Public domain name",
				EnvVars: []string{"DOMAIN"},
			},
			&cli.StringFlag{
				Name:    "email",
				Usage:   "Email address for Let's Encrypt",
				EnvVars: []string{"EMAIL"},
			},
			&cli.StringFlag{
				Name:    "token",
				Usage:   "Bearer token for authentication",
				EnvVars: []string{"TOKEN"},
			},
			&cli.StringFlag{
				Name:    "token-file",
				Usage:   "File containing bearer token for authentication",
				EnvVars: []string{"TOKEN_FILE"},
			},
			&cli.StringFlag{
				Name:    "cache",
				Usage:   "Directory for local cache",
				EnvVars: []string{"CACHE"},
				Value:   defaultCacheDir,
			},
			&cli.StringFlag{
				Name:    "cache-size",
				Usage:   "Maximum size of local cache",
				EnvVars: []string{"CACHE_SIZE"},
				Value:   "10G",
			},
			&cli.StringFlag{
				Name:    "hash-secret",
				Usage:   "Secret for secure hash",
				EnvVars: []string{"HASH_SECRET"},
			},
			&cli.StringFlag{
				Name:    "hash-secret-file",
				Usage:   "File containing secret for secure hash",
				EnvVars: []string{"HASH_SECRET_FILE"},
			},
			&cli.StringFlag{
				Name:     "webdav-uri",
				Usage:    "URI for WebDAV upstream",
				EnvVars:  []string{"WEBDAV_URI"},
				Required: true,
			},
			&cli.StringFlag{
				Name:     "webdav-user",
				Usage:    "Username for WebDAV upstream",
				EnvVars:  []string{"WEBDAV_USER"},
				Required: true,
			},
			&cli.StringFlag{
				Name:    "webdav-password",
				Usage:   "Password for WebDAV upstream",
				EnvVars: []string{"WEBDAV_PASSWORD"},
			},
			&cli.StringFlag{
				Name:    "webdav-password-file",
				Usage:   "File containing password for WebDAV upstream",
				EnvVars: []string{"WEBDAV_PASSWORD_FILE"},
			},
		},
		Action: func(cCtx *cli.Context) error {
			token := cCtx.String("token")
			if cCtx.IsSet("token-file") {
				data, err := os.ReadFile(cCtx.String("token-file"))
				if err != nil {
					return fmt.Errorf("failed to read token file: %w", err)
				}

				token = strings.TrimSpace(string(data))
			}

			if token == "" {
				return fmt.Errorf("authentication token is required")
			}

			webdavPassword := cCtx.String("webdav-password")
			if cCtx.IsSet("webdav-password-file") {
				data, err := os.ReadFile(cCtx.String("webdav-password-file"))
				if err != nil {
					return fmt.Errorf("failed to read WebDAV password file: %w", err)
				}

				webdavPassword = strings.TrimSpace(string(data))
			}

			if webdavPassword == "" {
				return fmt.Errorf("WebDAV password is required")
			}

			secureHashSecret := cCtx.String("hash-secret")
			if cCtx.IsSet("hash-secret-file") {
				data, err := os.ReadFile(cCtx.String("hash-secret-file"))
				if err != nil {
					return fmt.Errorf("failed to read secure hash secret file: %w", err)
				}

				secureHashSecret = strings.TrimSpace(string(data))
			}

			if secureHashSecret == "" {
				return fmt.Errorf("secure hash secret is required")
			}

			ups, err := upstream.NewWebDAV(cCtx.String("webdav-uri"),
				cCtx.String("webdav-user"), webdavPassword)
			if err != nil {
				return fmt.Errorf("failed to create WebDAV upstream: %w", err)
			}

			baseURL := fmt.Sprintf("https://%s/blobs", cCtx.String("domain"))
			if cCtx.Bool("dev") {
				baseURL = "http://localhost:8080/blobs"
			}

			cacheMaxBytes, err := units.FromHumanSize(cCtx.String("cache-size"))
			if err != nil {
				return fmt.Errorf("unable to parse cache size: %w", err)
			}

			storage, err := cas.NewStorage(cCtx.Context, logger, cCtx.String("cache"), cacheMaxBytes, []byte(secureHashSecret), baseURL, ups)
			if err != nil {
				return fmt.Errorf("failed to create content addressable storage handler: %w", err)
			}

			e := echo.New()
			e.Use(middleware.Recover())
			e.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
				Format: "method=${method}, uri=${uri}, status=${status}\n",
			}))

			e.GET("/blobs/:id/:name", storage.Get)
			e.POST("/blob", storage.Put, validBearerToken(token))

			if cCtx.Bool("dev") {
				logger.Info("Listening for connections")

				if err := e.Start(":8080"); err != http.ErrServerClosed {
					return fmt.Errorf("failed to start server: %w", err)
				}
			} else {
				if !cCtx.IsSet("email") {
					return fmt.Errorf("email address is required when not in development mode")
				}

				autoTLSManager := autocert.Manager{
					Prompt:     autocert.AcceptTOS,
					Cache:      autocert.DirCache("/var/www/.cache"),
					HostPolicy: autocert.HostWhitelist(cCtx.String("domain")),
					Email:      cCtx.String("email"),
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
						ServerName:     cCtx.String("domain"),
						GetCertificate: autoTLSManager.GetCertificate,
						NextProtos:     []string{acme.ALPNProto},
					},
				}

				logger.Info("Listening for connections")

				if err := s.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
					return fmt.Errorf("failed to start server: %w", err)
				}
			}

			return nil
		},
	}

	if err := app.Run(os.Args); err != nil {
		logger.Fatal("Failed to run application", zap.Error(err))
	}
}

func validBearerToken(token string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			if c.Request().Header.Get("Authorization") != "Bearer "+token {
				return echo.NewHTTPError(http.StatusUnauthorized)
			}

			return next(c)
		}
	}
}
