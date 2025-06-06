// Initial Copyright (c) 2023 Xenit AB and 2024 The Spegel Authors.
// Portions Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"

	pcontext "github.com/azure/peerd/pkg/context"
	"github.com/azure/peerd/pkg/discovery/routing"
	"github.com/azure/peerd/pkg/metrics"
	"github.com/azure/peerd/pkg/peernet"
)

var (
	// ResolveRetries is the number of times to attempt resolving a key before giving up.
	ResolveRetries = 3

	// ResolveTimeout is the timeout for resolving a key.
	ResolveTimeout = 1 * time.Second
)

// Mirror is a handler that handles requests to this registry proxy.
type Mirror struct {
	resolveTimeout time.Duration
	router         routing.Router
	resolveRetries int

	n               peernet.Network
	metricsRecorder metrics.Metrics
}

// Handle handles a request to this registry mirror.
func (m *Mirror) Handle(c pcontext.Context) {
	key := c.GetString(pcontext.DigestCtxKey)
	if key == "" {
		key = c.GetString(pcontext.ReferenceCtxKey)
	}

	l := pcontext.Logger(c).With().Str("handler", "mirror").Str("ref", key).Logger()
	l.Debug().Msg("mirror handler start")
	s := time.Now()
	defer func() {
		l.Debug().Dur("duration", time.Since(s)).Msg("mirror handler stop")
	}()

	// Resolve mirror with the requested key
	resolveCtx, cancel := context.WithTimeout(c, m.resolveTimeout)
	defer cancel()

	if key == "" {
		// nolint
		c.AbortWithError(http.StatusInternalServerError, errors.New("neither digest nor reference provided"))
	}

	startTime := time.Now()
	peerCount := 0
	peersChan, err := m.router.Resolve(resolveCtx, key, false, m.resolveRetries)
	if err != nil {
		//nolint
		c.AbortWithError(http.StatusInternalServerError, err)
	}

	for {
		select {

		case <-resolveCtx.Done():
			// Resolving mirror has timed out.
			//nolint
			c.AbortWithError(http.StatusNotFound, fmt.Errorf(pcontext.PeerNotFoundLog))
			return

		case peer, ok := <-peersChan:
			// Channel closed means no more mirrors will be received and max retries has been reached.
			if !ok {
				//nolint
				c.AbortWithError(http.StatusInternalServerError, fmt.Errorf(pcontext.PeerResolutionExhaustedLog))
				return
			}

			if peerCount == 0 {
				// Only report the time it took to discover the first peer.
				m.metricsRecorder.RecordPeerDiscovery(peer.HttpHost, time.Since(startTime).Seconds())
				peerCount++
			}

			succeeded := false
			u, err := url.Parse(peer.HttpHost)
			if err != nil {
				//nolint
				c.AbortWithError(http.StatusInternalServerError, err)
				return
			}

			proxy := httputil.NewSingleHostReverseProxy(u)
			proxy.Director = func(r *http.Request) {
				r.URL = u
				r.URL.Path = c.Request.URL.Path
				r.URL.RawQuery = c.Request.URL.RawQuery
				pcontext.SetOutboundHeaders(r, c)
			}

			count := int64(0)

			proxy.ModifyResponse = func(resp *http.Response) error {
				if resp.StatusCode != http.StatusOK {
					return fmt.Errorf("expected peer to respond with 200, got: %s", resp.Status)
				}

				succeeded = true
				count = resp.ContentLength
				return nil
			}
			proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
				l.Error().Err(err).Msg("peer request failed, attempting next")
			}
			proxy.Transport = m.n.RoundTripperFor(peer.ID)

			proxy.ServeHTTP(c.Writer, c.Request)
			if !succeeded {
				break
			}

			m.metricsRecorder.RecordPeerResponse(peer.HttpHost, key, "pull", time.Since(startTime).Seconds(), count)
			l.Info().Str("peer", u.Host).Int64("count", count).Msg("request served from peer")
			return
		}
	}
}

// New creates a new mirror handler.
func New(ctx context.Context, router routing.Router) *Mirror {
	return &Mirror{
		metricsRecorder: metrics.FromContext(ctx),
		resolveTimeout:  ResolveTimeout,
		router:          router,
		resolveRetries:  ResolveRetries,
		n:               router.Net(),
	}
}
