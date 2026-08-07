// Package archive encodes and decodes PortfolioDB archive documents.
//
// The encoding decisions are made here and only here. An archive is written
// today and read by a server that does not exist yet, so the options that
// decide what it looks like on disk -- proto field names, enums by name, absent
// fields staying absent -- cannot be restated at each call site: one caller
// forgetting UseProtoNames silently rewrites every key in the file.
//
// See docs/spec/archive-format.md.
package archive

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"buf.build/go/protovalidate"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
)

// FormatVersion is what this build writes, and the highest it will read.
const FormatVersion = 1

var marshalOpts = protojson.MarshalOptions{
	// snake_case keys. Without this protojson writes lowerCamelCase, which is a
	// total format change and a silent one.
	UseProtoNames: true,
	// Enums by name: "STOCK", not 1.
	UseEnumNumbers: false,
	// Absent stays absent, which is the whole point of the optional fields.
	EmitUnpopulated: false,
	AllowPartial:    false,
}

var unmarshalOpts = protojson.UnmarshalOptions{
	// A field added by a later PortfolioDB is dropped rather than failing the
	// parse. That is safe only because format_version is checked first: an
	// additive change leaves the version alone, and a change an older reader
	// cannot survive bumps it and is refused outright.
	DiscardUnknown: true,
	AllowPartial:   false,
}

// VersionError reports an archive written by a later PortfolioDB.
type VersionError struct {
	Got      uint32
	Supports uint32
}

func (e *VersionError) Error() string {
	return fmt.Sprintf("archive format version %d is newer than this server supports (%d): upgrade to read it", e.Got, e.Supports)
}

// KindError reports an archive handed to the wrong importer -- a user archive
// given to the admin path, or the reverse.
type KindError struct {
	Got  archivev1.ArchiveKind
	Want archivev1.ArchiveKind
}

func (e *KindError) Error() string {
	return fmt.Sprintf("archive is %s, expected %s", e.Got, e.Want)
}

// MarshalAdmin writes an admin archive. It stamps the envelope's format_version
// and kind itself, so a caller cannot write a document that misdescribes itself.
func MarshalAdmin(a *archivev1.AdminArchive) ([]byte, error) {
	a = proto.CloneOf(a)
	if a.Envelope == nil {
		a.Envelope = &archivev1.Envelope{}
	}
	a.Envelope.FormatVersion = FormatVersion
	a.Envelope.Kind = archivev1.ArchiveKind_ADMIN
	return marshal(a)
}

// MarshalUser writes a user archive.
func MarshalUser(a *archivev1.UserArchive) ([]byte, error) {
	a = proto.CloneOf(a)
	if a.Envelope == nil {
		a.Envelope = &archivev1.Envelope{}
	}
	a.Envelope.FormatVersion = FormatVersion
	a.Envelope.Kind = archivev1.ArchiveKind_USER
	return marshal(a)
}

// UnmarshalAdmin reads an admin archive.
func UnmarshalAdmin(b []byte) (*archivev1.AdminArchive, error) {
	if err := probe(b, archivev1.ArchiveKind_ADMIN); err != nil {
		return nil, err
	}
	var a archivev1.AdminArchive
	if err := unmarshal(b, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

// UnmarshalUser reads a user archive.
func UnmarshalUser(b []byte) (*archivev1.UserArchive, error) {
	if err := probe(b, archivev1.ArchiveKind_USER); err != nil {
		return nil, err
	}
	var a archivev1.UserArchive
	if err := unmarshal(b, &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func marshal(m proto.Message) ([]byte, error) {
	if err := protovalidate.Validate(m); err != nil {
		return nil, fmt.Errorf("invalid archive: %w", err)
	}
	b, err := marshalOpts.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal archive: %w", err)
	}
	// protojson injects unstable whitespace on purpose, so that callers cannot
	// depend on its exact bytes. An archive is a file a user diffs and a script
	// writes, so normalise it: two exports of the same data are then identical.
	var out bytes.Buffer
	if err := json.Compact(&out, b); err != nil {
		return nil, fmt.Errorf("compact archive: %w", err)
	}
	return out.Bytes(), nil
}

func unmarshal(b []byte, m proto.Message) error {
	if err := unmarshalOpts.Unmarshal(b, m); err != nil {
		return fmt.Errorf("parse archive: %w", err)
	}
	if err := protovalidate.Validate(m); err != nil {
		return fmt.Errorf("invalid archive: %w", err)
	}
	return nil
}

// probe reads the envelope with encoding/json before the real parse, because
// the real parse is what a version mismatch would break: a reader that failed
// on an unknown field would report a confusing parse error where it should
// report that the file was written by a later PortfolioDB.
//
// It accepts both spellings of the key. protojson writes snake_case and reads
// either, so a hand-written document using lowerCamelCase parses.
func probe(b []byte, want archivev1.ArchiveKind) error {
	var p struct {
		Envelope struct {
			FormatVersion  uint32 `json:"format_version"`
			FormatVersionC uint32 `json:"formatVersion"`
			Kind           string `json:"kind"`
		} `json:"envelope"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return fmt.Errorf("not an archive document: %w", err)
	}
	v := p.Envelope.FormatVersion
	if v == 0 {
		v = p.Envelope.FormatVersionC
	}
	if v == 0 {
		return errors.New("archive has no format_version")
	}
	if v > FormatVersion {
		return &VersionError{Got: v, Supports: FormatVersion}
	}
	got, ok := archivev1.ArchiveKind_value[p.Envelope.Kind]
	if !ok {
		return fmt.Errorf("archive kind %q is not a known kind", p.Envelope.Kind)
	}
	if archivev1.ArchiveKind(got) != want {
		return &KindError{Got: archivev1.ArchiveKind(got), Want: want}
	}
	return nil
}
