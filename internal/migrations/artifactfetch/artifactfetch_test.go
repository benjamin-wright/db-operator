package artifactfetch

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestFetch_extractsArtifact(t *testing.T) {
	files := map[string]string{
		"0001-init-apply.sql":    "CREATE TABLE foo();",
		"0001-init-rollback.sql": "DROP TABLE foo;",
	}
	srv, ref := newRegistry(t, files, MediaType)
	defer srv.Close()

	dest := t.TempDir()
	dgst, err := Fetch(context.Background(), ref, dest)
	if err != nil {
		t.Fatalf("Fetch returned error: %v", err)
	}
	if !strings.HasPrefix(dgst, "sha256:") {
		t.Fatalf("expected sha256 digest, got %q", dgst)
	}

	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if string(got) != want {
			t.Fatalf("file %s content mismatch:\nwant: %q\ngot:  %q", name, want, string(got))
		}
	}
}

func TestFetch_rejectsWrongMediaType(t *testing.T) {
	files := map[string]string{"x.sql": "SELECT 1;"}
	srv, ref := newRegistry(t, files, "application/vnd.bogus.tar+gzip")
	defer srv.Close()

	_, err := Fetch(context.Background(), ref, t.TempDir())
	if err == nil {
		t.Fatal("expected error for wrong media type, got nil")
	}
	if !strings.Contains(err.Error(), MediaType) {
		t.Fatalf("error %q should mention the expected media type", err)
	}
}

func TestFetch_rejectsPathTraversal(t *testing.T) {
	cases := map[string]map[string]string{
		"absolute path": {"/etc/escape.sql": "evil"},
		"deep parent escape via subdir": {
			"sub/../../escape.sql": "evil",
		},
	}
	for name, files := range cases {
		t.Run(name, func(t *testing.T) {
			srv, ref := newRegistry(t, files, MediaType)
			defer srv.Close()

			_, err := Fetch(context.Background(), ref, t.TempDir())
			if err == nil || !strings.Contains(err.Error(), "escapes destination") {
				t.Fatalf("expected escape error, got %v", err)
			}
		})
	}
}

// newRegistry spins up an in-memory OCI distribution v2 endpoint that serves a
// single artifact built from files. It returns the test server and the
// repository reference (host:port/test:v1) that Fetch can use.
func newRegistry(t *testing.T, files map[string]string, layerMediaType string) (*httptest.Server, string) {
	t.Helper()

	layer := buildTarGz(t, files)
	layerDigest := digest.FromBytes(layer)

	configBytes := []byte("{}")
	configDigest := digest.FromBytes(configBytes)

	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{SchemaVersion: 2},
		MediaType: ocispec.MediaTypeImageManifest,
		Config: ocispec.Descriptor{
			MediaType: "application/vnd.db-operator.migrations.config.v1+json",
			Digest:    configDigest,
			Size:      int64(len(configBytes)),
		},
		Layers: []ocispec.Descriptor{{
			MediaType: layerMediaType,
			Digest:    layerDigest,
			Size:      int64(len(layer)),
		}},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshalling manifest: %v", err)
	}
	manifestDigest := digest.FromBytes(manifestBytes)

	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/":
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/v2/test/manifests/v1" || r.URL.Path == "/v2/test/manifests/"+manifestDigest.String():
			w.Header().Set("Content-Type", ocispec.MediaTypeImageManifest)
			w.Header().Set("Docker-Content-Digest", manifestDigest.String())
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodGet {
				_, _ = w.Write(manifestBytes)
			}
		case r.URL.Path == "/v2/test/blobs/"+configDigest.String():
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", configDigest.String())
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodGet {
				_, _ = w.Write(configBytes)
			}
		case r.URL.Path == "/v2/test/blobs/"+layerDigest.String():
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Header().Set("Docker-Content-Digest", layerDigest.String())
			w.WriteHeader(http.StatusOK)
			if r.Method == http.MethodGet {
				_, _ = w.Write(layer)
			}
		default:
			t.Logf("unexpected registry request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})

	srv := httptest.NewServer(mux)
	host := strings.TrimPrefix(srv.URL, "http://")
	ref := host + "/test:v1"

	t.Setenv("ORAS_PLAIN_HTTP", "true")
	return srv, ref
}

func buildTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("writing tar header: %v", err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("writing tar body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("closing tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("closing gzip: %v", err)
	}
	return buf.Bytes()
}
