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
	"google.golang.org/protobuf/types/known/timestamppb"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
)

// FormatVersion is what this build writes, and the highest it will read.
//
// 2: a listing is named by a security identifier and a currency, so InstrumentRef
// carries one and PriceGroup's own currency field is gone -- a field removed and
// a message renumbered, which is what the version exists to catch.
const FormatVersion = 2

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
// given to the system path, or the reverse.
type KindError struct {
	Got  archivev1.ArchiveKind
	Want archivev1.ArchiveKind
}

func (e *KindError) Error() string {
	return fmt.Sprintf("archive is %s, expected %s", e.Got, e.Want)
}

// NewEnvelope builds the envelope an export stamps onto its own document. The
// format version and the kind come from this build rather than from a caller,
// for the same reason MarshalSystem overwrites them.
//
// exported_at is knowledge time -- when the data being exported was current --
// so it is the exporting server's clock and nothing else's.
func NewEnvelope(sourceInstance string, kind archivev1.ArchiveKind) *archivev1.Envelope {
	return &archivev1.Envelope{
		FormatVersion:  FormatVersion,
		ExportedAt:     timestamppb.Now(),
		SourceInstance: sourceInstance,
		Kind:           kind,
	}
}

// CheckEnvelope validates an envelope that arrived over the API rather than
// inside a file. UnmarshalSystem probes the bytes before parsing them, but an
// RPC carrying an already-parsed envelope has no bytes to probe, and the two
// refusals -- a file from a later PortfolioDB, a file handed to the wrong
// importer -- have to happen either way.
func CheckEnvelope(e *archivev1.Envelope, want archivev1.ArchiveKind) error {
	if e == nil {
		return errors.New("archive has no envelope")
	}
	if e.GetFormatVersion() == 0 {
		return errors.New("archive has no format_version")
	}
	if e.GetFormatVersion() > FormatVersion {
		return &VersionError{Got: e.GetFormatVersion(), Supports: FormatVersion}
	}
	if e.GetKind() != want {
		return &KindError{Got: e.GetKind(), Want: want}
	}
	return nil
}

// MarshalSystem writes a system archive. It stamps the envelope's format_version
// and kind itself, so a caller cannot write a document that misdescribes itself.
func MarshalSystem(a *archivev1.SystemArchive) ([]byte, error) {
	a = proto.CloneOf(a)
	if a.Envelope == nil {
		a.Envelope = &archivev1.Envelope{}
	}
	a.Envelope.FormatVersion = FormatVersion
	a.Envelope.Kind = archivev1.ArchiveKind_SYSTEM
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

// UnmarshalSystem reads a system archive.
func UnmarshalSystem(b []byte) (*archivev1.SystemArchive, error) {
	if err := probe(b, archivev1.ArchiveKind_SYSTEM); err != nil {
		return nil, err
	}
	var a archivev1.SystemArchive
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
