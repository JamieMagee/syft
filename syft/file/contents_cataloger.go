package file

import (
	"bytes"
	"encoding/base64"
	"io"

	"github.com/anchore/syft/internal"

	"github.com/anchore/syft/internal/log"
	"github.com/anchore/syft/syft/source"
)

type ContentsCataloger struct {
	globs                     []string
	skipFilesAboveSizeInBytes int64
}

func NewContentsCataloger(globs []string, skipFilesAboveSize int64) (*ContentsCataloger, error) {
	return &ContentsCataloger{
		globs:                     globs,
		skipFilesAboveSizeInBytes: skipFilesAboveSize,
	}, nil
}

func (i *ContentsCataloger) Catalog(resolver source.FileResolver) (map[source.Location]string, error) {
	results := make(map[source.Location]string)
	var locations []source.Location

	locations, err := resolver.FilesByGlob(i.globs...)
	if err != nil {
		return nil, err
	}
	for _, location := range locations {
		metadata, err := resolver.FileMetadataByLocation(location)
		if err != nil {
			return nil, err
		}

		if i.skipFilesAboveSizeInBytes > 0 && metadata.Size > i.skipFilesAboveSizeInBytes {
			continue
		}

		result, err := i.catalogLocation(resolver, location)
		if internal.IsErrPathPermission(err) {
			log.Debugf("file contents cataloger skipping - %+v", err)
			continue
		}
		if err != nil {
			return nil, err
		}
		results[location] = result
	}
	log.Debugf("file contents cataloger processed %d files", len(results))

	return results, nil
}

func (i *ContentsCataloger) catalogLocation(resolver source.FileResolver, location source.Location) (string, error) {
	contentReader, err := resolver.FileContentsByLocation(location)
	if err != nil {
		return "", err
	}
	defer internal.CloseAndLogError(contentReader, location.VirtualPath)

	buf := &bytes.Buffer{}
	if _, err = io.Copy(base64.NewEncoder(base64.StdEncoding, buf), contentReader); err != nil {
		return "", internal.ErrPath{Path: location.RealPath, Err: err}
	}

	return buf.String(), nil
}
