package agentdocs

import "errors"

// Sentinel errors, one per failure class. Every exported operation returns an
// *Error wrapping one of them, so callers use errors.Is.
var (
	// ErrManifestInvalid is a manifest that cannot be read or decoded, names
	// an unknown schema, declares no groups or no sections, or lists one
	// command path twice.
	ErrManifestInvalid = errors.New("docs manifest is invalid")
	// ErrUnknownTopic is a topic no section carries. The message names every
	// valid topic.
	ErrUnknownTopic = errors.New("no such topic")
	// ErrPageMissing is a section whose page the tree does not hold: a
	// corpus/binary mismatch, not a caller mistake.
	ErrPageMissing = errors.New("the manifest names a page the tree does not hold")
	// ErrTreeMissing is an Install whose dir is not a directory in the tree.
	// Nothing is written or removed.
	ErrTreeMissing = errors.New("the tree holds no such directory")
	// ErrToolID is an Install whose Tool has no ID, so the install marker
	// would have no namespace. Nothing is written or removed.
	ErrToolID = errors.New("tool has no ID; the install marker needs a namespace")
	// ErrForeignTarget is an Install target or a Link entry that exists, is not
	// empty, and is not this tool's. Nothing is written unless forced.
	ErrForeignTarget = errors.New("target exists and is not this tool's")
	// ErrInvalidName is a Link name that is not one path element.
	ErrInvalidName = errors.New("name must be one path element")
	// ErrPointerMalformed is a context file whose markers do not delimit
	// exactly one block, or a body that carries the markers itself. Nothing is
	// written.
	ErrPointerMalformed = errors.New("pointer markers do not delimit exactly one block")
)

// Error carries the operation and the input that failed.
type Error struct {
	Op    string
	Input string
	Err   error
}

func (e *Error) Error() string {
	if e.Input == "" {
		return "agentdocs: " + e.Op + ": " + e.Err.Error()
	}
	return "agentdocs: " + e.Op + ": " + e.Input + ": " + e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }
