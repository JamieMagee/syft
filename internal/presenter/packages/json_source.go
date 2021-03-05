package packages

import (
	"encoding/json"
	"fmt"

	"github.com/anchore/syft/syft/source"
)

// JsonSource object represents the thing that was cataloged
type JsonSource struct {
	Type   string      `json:"type"`
	Target interface{} `json:"target"`
}

// jsonSourceUnpacker is used to unmarshal JsonSource objects
type jsonSourceUnpacker struct {
	Type   string          `json:"type"`
	Target json.RawMessage `json:"target"`
}

type JsonImageMetadata struct {
	Scope source.Scope
	source.ImageMetadata
}

// NewJsonSource creates a new source object to be represented into JSON.
func NewJsonSource(src source.Metadata, scope source.Scope) (JsonSource, error) {
	switch src.Scheme {
	case source.ImageScheme:
		return JsonSource{
			Type: "image",
			Target: JsonImageMetadata{
				Scope:         scope,
				ImageMetadata: src.ImageMetadata,
			},
		}, nil
	case source.DirectoryScheme:
		return JsonSource{
			Type:   "directory",
			Target: src.Path,
		}, nil
	default:
		return JsonSource{}, fmt.Errorf("unsupported source: %T", src)
	}
}

// UnmarshalJSON populates a source object from JSON bytes.
func (s *JsonSource) UnmarshalJSON(b []byte) error {
	var unpacker jsonSourceUnpacker
	if err := json.Unmarshal(b, &unpacker); err != nil {
		return err
	}

	s.Type = unpacker.Type

	switch s.Type {
	case "image":
		var payload source.ImageMetadata
		if err := json.Unmarshal(unpacker.Target, &payload); err != nil {
			return err
		}
		s.Target = payload
	default:
		return fmt.Errorf("unsupported package metadata type: %+v", s.Type)
	}

	return nil
}

// ToSourceMetadata takes a source object represented from JSON and creates a source.Metadata object.
func (s *JsonSource) ToSourceMetadata() source.Metadata {
	var metadata source.Metadata
	switch s.Type {
	case "directory":
		metadata.Scheme = source.DirectoryScheme
		metadata.Path = s.Target.(string)
	case "image":
		metadata.Scheme = source.ImageScheme
		metadata.ImageMetadata = s.Target.(source.ImageMetadata)
	}
	return metadata
}
