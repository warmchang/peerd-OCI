// Copyright (c) Microsoft Corporation.
// Licensed under the MIT License.
package store

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/azure/peerd/pkg/cache"
	pcontext "github.com/azure/peerd/pkg/context"
	"github.com/azure/peerd/pkg/discovery/content/reader"
	"github.com/azure/peerd/pkg/discovery/routing"
	"github.com/azure/peerd/pkg/files"
	"github.com/azure/peerd/pkg/metrics"
	"github.com/azure/peerd/pkg/urlparser"
	"github.com/opencontainers/go-digest"
	"github.com/rs/zerolog"
)

const DefaultFileCachePath = "/tmp/distribution/peerd/cache"

// NewFilesStore creates a new store.
func NewFilesStore(ctx context.Context, r routing.Router, fileCachePath string) (FilesStore, error) {
	fs := &store{
		metricsRecorder: metrics.FromContext(ctx),
		cache:           cache.NewCache(ctx, int64(files.CacheBlockSize), fileCachePath),
		prefetchChan:    make(chan prefetchableSegment, PrefetchWorkers),
		prefetchable:    PrefetchWorkers > 0,
		router:          r,
		resolveRetries:  ResolveRetries,
		resolveTimeout:  ResolveTimeout,
		blobsChan:       make(chan string, 1000),
		parser:          urlparser.New(),
	}

	go func() {
		<-ctx.Done()
		err := r.Close()
		l := zerolog.Ctx(ctx).Debug()
		if err != nil {
			l = zerolog.Ctx(ctx).Error().Err(err)
		}
		l.Msg("router close")
	}()

	for i := 0; i < PrefetchWorkers; i++ {
		go fs.prefetch()
	}

	return fs, nil
}

// prefetchableSegment describes a part of a file to prefetch.
type prefetchableSegment struct {
	name   string
	offset int64
	count  int

	reader reader.Reader
}

// store describes a content store whose contents can come from disk or a remote source.
type store struct {
	metricsRecorder metrics.Metrics
	cache           cache.Cache
	prefetchable    bool
	prefetchChan    chan prefetchableSegment
	router          routing.Router
	resolveRetries  int
	resolveTimeout  time.Duration
	blobsChan       chan string
	parser          urlparser.Parser
}

var _ FilesStore = &store{}

// Subscribe returns a channel that will be notified when a blob is added to the store.
func (s *store) Subscribe() chan string {
	return s.blobsChan
}

// Open opens the requested file and starts prefetching it.
func (s *store) Open(c pcontext.Context) (File, error) {

	chunkKey := c.GetString(pcontext.FileChunkCtxKey)
	tokens := strings.Split(chunkKey, files.FileChunkKeySep)
	name := tokens[0]
	alignedOff, _ := strconv.ParseInt(tokens[1], 10, 64)

	log := pcontext.Logger(c)
	if pcontext.IsRequestFromAPeer(c) {
		// This request came from a peer. Don't serve it unless we have the requested range cached.
		if ok := s.cache.Exists(name, alignedOff); !ok {
			log.Info().Str("name", name).Msg("peer request not cached")
			return nil, os.ErrNotExist
		}
	}

	f := &file{
		Name:   name,
		store:  s,
		cur:    0,
		size:   0,
		reader: reader.NewReader(c, s.router, s.resolveRetries, s.resolveTimeout, s.metricsRecorder),
	}

	if pcontext.IsRequestFromAPeer(c) {
		// Ensure this file can only serve the requested chunk.
		// This is to prevent infinite loops when a peer requests a file that is not cached.
		f.chunkOffset = alignedOff
	}

	fileSize, err := f.Fstat() // Fstat sets up the file size appropriately.

	if s.prefetchable {
		f.prefetch(0, fileSize)
	}

	return f, err
}

// Key tries to find the cache key for the requested content or returns empty.
func (s *store) Key(c pcontext.Context) (string, digest.Digest, error) {
	log := pcontext.Logger(c)

	blobUrl := pcontext.BlobUrl(c)
	d, err := s.parser.ParseDigest(blobUrl)
	if err != nil {
		log.Error().Err(err).Msg("store key")
	}

	startIndex := int64(0) // Default to 0 for HEADs.
	if c.Request.Method == "GET" {
		startIndex, err = pcontext.RangeStartIndex(c.Request.Header.Get("Range"))
		if err != nil {
			return "", "", err
		}
	}
	key := files.FileChunkKey(d.String(), startIndex, int64(files.CacheBlockSize))

	log.Info().Str("digest", d.String()).Str("key", key).Msg("store key")
	return key, d, err
}

// prefetch prefetches files.
func (s *store) prefetch() {
	for p := range s.prefetchChan {
		if _, err := s.cache.GetOrCreate(p.name, p.offset, p.count, func() ([]byte, error) {
			return files.FetchFile(p.reader, p.name, p.offset, p.count)
		}); err != nil {
			p.reader.Log().Error().Err(err).Str("name", p.name).Msg("prefetch failed")
		} else {
			// Advertise the chunk.
			s.blobsChan <- files.FileChunkKey(p.name, p.offset, int64(files.CacheBlockSize))
		}
	}
}
