// Package artifactfetch pulls db-operator migration artifacts from an
// OCI-compatible registry using the ORAS protocol.
//
// A migration artifact is an OCI manifest with exactly one layer whose
// media type is MediaType. The layer payload is a gzipped tar archive
// containing the migration SQL files (no leading directory). Fetch resolves
// the supplied reference to a digest, downloads the layer, and extracts the
// archive into dest. The manifest digest is returned so callers can record
// the precise content their migrations were applied from.
package artifactfetch

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
	"oras.land/oras-go/v2/registry/remote/retry"
)

// MediaType identifies the migration tarball layer inside an ORAS artifact.
const MediaType = "application/vnd.db-operator.migrations.v1.tar+gzip"

// MaxLayerSize bounds the size of the artifact layer that will be downloaded
// and extracted. 256 MiB is generous for SQL migration sets while still
// preventing memory- or disk-exhaustion from a hostile registry.
const MaxLayerSize = 256 << 20

// Fetch pulls the artifact identified by ref into dest, returning the
// resolved manifest digest (e.g. "sha256:…") on success. dest must already
// exist. The artifact must consist of exactly one layer with MediaType.
func Fetch(ctx context.Context, ref, dest string) (string, error) {
	repo, err := remote.NewRepository(ref)
	if err != nil {
		return "", fmt.Errorf("parsing artifact reference %q: %w", ref, err)
	}

	if isLoopback(repo.Reference.Registry) {
		repo.PlainHTTP = true
	}

	credStore, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
	if err == nil {
		repo.Client = &auth.Client{
			Client:     retry.DefaultClient,
			Cache:      auth.NewCache(),
			Credential: credentials.Credential(credStore),
		}
	}

	tag := repo.Reference.Reference
	if tag == "" {
		return "", fmt.Errorf("artifact reference %q is missing a tag or digest", ref)
	}

	manifestDesc, manifestBytes, err := oras.FetchBytes(ctx, repo, tag, oras.DefaultFetchBytesOptions)
	if err != nil {
		return "", fmt.Errorf("fetching manifest for %q: %w", ref, err)
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return "", fmt.Errorf("parsing manifest for %q: %w", ref, err)
	}

	layer, err := selectLayer(manifest.Layers)
	if err != nil {
		return "", err
	}

	if layer.Size > MaxLayerSize {
		return "", fmt.Errorf("layer size %d exceeds maximum %d", layer.Size, MaxLayerSize)
	}

	rc, err := content.FetchAll(ctx, repo, layer)
	if err != nil {
		return "", fmt.Errorf("fetching layer %s: %w", layer.Digest, err)
	}

	if err := extractTarGz(rc, dest); err != nil {
		return "", fmt.Errorf("extracting layer %s into %s: %w", layer.Digest, dest, err)
	}

	return manifestDesc.Digest.String(), nil
}

func selectLayer(layers []ocispec.Descriptor) (ocispec.Descriptor, error) {
	var matches []ocispec.Descriptor
	for _, l := range layers {
		if l.MediaType == MediaType {
			matches = append(matches, l)
		}
	}
	switch len(matches) {
	case 0:
		return ocispec.Descriptor{}, fmt.Errorf("artifact has no layer with media type %s", MediaType)
	case 1:
		return matches[0], nil
	default:
		return ocispec.Descriptor{}, fmt.Errorf("artifact has %d layers with media type %s; expected exactly 1", len(matches), MediaType)
	}
}

func extractTarGz(blob []byte, dest string) error {
	gz, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return fmt.Errorf("opening gzip stream: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reading tar entry: %w", err)
		}

		target, err := safeJoin(dest, hdr.Name)
		if err != nil {
			return err
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("creating directory %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("creating parent of %s: %w", target, err)
			}
			if err := writeRegular(target, tr, hdr.Size); err != nil {
				return err
			}
		default:
			// Skip symlinks, devices, FIFOs, etc — migration tarballs only need
			// regular files and directories.
			continue
		}
	}
}

func writeRegular(path string, src io.Reader, size int64) error {
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("opening %s for write: %w", path, err)
	}
	defer out.Close()

	if _, err := io.CopyN(out, src, size); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// safeJoin returns dest/name only when name resolves inside dest. Absolute
// paths are rejected outright; relative paths are normalised and verified to
// stay within dest after joining.
func safeJoin(dest, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("tar entry has empty name")
	}
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("tar entry %q escapes destination", name)
	}
	target := filepath.Join(dest, filepath.Clean(name))
	absDest, err := filepath.Abs(dest)
	if err != nil {
		return "", err
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if absTarget != absDest && !strings.HasPrefix(absTarget, absDest+string(filepath.Separator)) {
		return "", fmt.Errorf("tar entry %q escapes destination", name)
	}
	return target, nil
}

// isLoopback reports whether host (with optional :port) refers to a loopback
// address. We auto-enable plain HTTP for loopback registries so that local
// testing and dev clusters do not require a TLS cert.
func isLoopback(host string) bool {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		host = host[:i]
	}
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return strings.HasSuffix(host, ".localhost")
}
