// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/azure/peerd/pkg/containerd"
	"github.com/azure/peerd/pkg/discovery/routing/mocks"
	"github.com/azure/peerd/pkg/files/store"
	"github.com/azure/peerd/pkg/metrics"
	"github.com/gin-gonic/gin"
)

var (
	simpleOKHandler = gin.HandlerFunc(func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	ctxWithMetrics, _ = metrics.WithContext(context.Background(), "test", "peerd")
)

func TestV2RoutesRegistrations(t *testing.T) {
	recorder := httptest.NewRecorder()
	mc, me := gin.CreateTestContext(recorder)
	registerRoutes(me, nil, simpleOKHandler)

	tests := []struct {
		name           string
		method         string
		path           string
		expectedStatus int
	}{
		{
			name:           "root",
			method:         http.MethodGet,
			path:           "/v2",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "root head",
			method:         http.MethodHead,
			path:           "/v2",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "manifests",
			method:         http.MethodGet,
			path:           "/v2/azure-cli/manifests/latest?ns=registry.k8s.io",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "manifests nested",
			method:         http.MethodGet,
			path:           "/v2/azure-cli/with/a/nested/component/manifests/latest?ns=registry.k8s.io",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "blobs",
			method:         http.MethodGet,
			path:           "/v2/azure-cli/blobs/sha256:1234?ns=registry.k8s.io",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "blobs nested",
			method:         http.MethodGet,
			path:           "/v2/azure-cli/with/a/nested/component/blobs/sha256:1234?ns=registry.k8s.io",
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, tt.path, nil)
			if err != nil {
				t.Fatal(err)
			}

			me.ServeHTTP(mc.Writer, req)

			if recorder.Code != http.StatusOK {
				t.Errorf("%s: expected status code %d, got %d", tt.name, http.StatusOK, recorder.Code)
			}
		})
	}
}

func TestNewEngine(t *testing.T) {
	engine := newEngine(ctxWithMetrics)
	if engine == nil {
		t.Fatal("Expected non-nil engine, got nil")
	}

	if engine.Handlers == nil {
		t.Fatal("Expected non-nil handlers, got nil")
	}

	if len(engine.Handlers) != 2 {
		t.Errorf("Expected 2 middleware, got %d", len(engine.Handlers))
	}
}

func TestHandler(t *testing.T) {
	mr := mocks.NewMockRouter(map[string][]string{})
	ms := containerd.NewMockContainerdStore(nil)
	mfs, err := store.NewMockStore(ctxWithMetrics, mr, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	h, err := Handler(ctxWithMetrics, mr, ms, mfs)
	if err != nil {
		t.Fatal(err)
	}

	if h == nil {
		t.Fatal("Expected non-nil handler, got nil")
	}
}
