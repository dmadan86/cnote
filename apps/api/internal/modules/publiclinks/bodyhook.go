package publiclinks

import (
	"errors"
	"sort"
	"sync"
)

// The public body rewriter (docs-parity-b 4.2 and 13.2.11). What a public page serves is the stored Markdown after every
// registered pass has rewritten it, so that no identifier of a workspace, person or other page, and no file of another page,
// is left in what an anonymous reader receives.
//
// Ownership: P2 (publiclinks/pagebody.go) implements the core pass: files of this page, mentions, links under /w/ and /api/,
// children marker, merged-table HTML. A later phase that adds a new stored form (B2's [[children]] line, a page-mention
// node, ...) adds its own pass from its own file with RegisterBodyPass and never edits P2's file.
//
// Fail closed: PublicBody refuses to run unless the core pass is registered, and any pass error or panic is an error the
// endpoint turns into the one 404. A pass sees and returns the whole body, so passes run in order, core first.

// BodyScope is what a pass may know about the request. Passes get no database handle: the lookups they need (which files
// belong to this page, which are used by the body) are resolved by the caller before the rewrite and handed in.
type BodyScope struct {
	WorkspaceID string
	NoteID      string
	Token       string          // the link's token, only for building /api/public/page/<token>/files/<id>
	OwnFiles    map[string]bool // ids of the files attached to this page
}

// BodyRewriter rewrites one body. It must be pure (same input, same output) and must not return text that contains an
// identifier it removed. Rewrite returns the new body or an error; it never returns the input on error.
type BodyRewriter interface {
	Name() string
	Rewrite(md string, s BodyScope) (string, error)
}

// ErrNoCoreBodyPass means PublicBody ran before the core pass was registered: nothing is served.
var ErrNoCoreBodyPass = errors.New("public body: the core rewriter is not registered")

type regPass struct {
	order int
	p     BodyRewriter
}

var (
	bodyMu     sync.RWMutex
	bodyCore   BodyRewriter
	bodyPasses []regPass
)

// RegisterCoreBodyPass registers the one core pass, which runs first. A second registration panics: two cores would mean
// two owners of the same rules. Call from an init().
func RegisterCoreBodyPass(p BodyRewriter) {
	bodyMu.Lock()
	defer bodyMu.Unlock()
	if bodyCore != nil {
		panic("publiclinks: core body pass registered twice: " + bodyCore.Name() + " and " + p.Name())
	}
	bodyCore = p
}

// RegisterBodyPass adds a pass that runs after the core pass, lowest order first, then by name, so the result does not depend
// on the order Go runs init functions in. Call from an init().
func RegisterBodyPass(order int, p BodyRewriter) {
	bodyMu.Lock()
	defer bodyMu.Unlock()
	bodyPasses = append(bodyPasses, regPass{order, p})
	sort.SliceStable(bodyPasses, func(i, j int) bool {
		if bodyPasses[i].order != bodyPasses[j].order {
			return bodyPasses[i].order < bodyPasses[j].order
		}
		return bodyPasses[i].p.Name() < bodyPasses[j].p.Name()
	})
}

// PublicBody is the only way to turn a stored body into a public one.
func PublicBody(md string, s BodyScope) (out string, err error) {
	bodyMu.RLock()
	core, rest := bodyCore, append([]regPass(nil), bodyPasses...)
	bodyMu.RUnlock()
	if core == nil {
		return "", ErrNoCoreBodyPass
	}
	defer func() {
		if r := recover(); r != nil {
			out, err = "", errors.New("public body: a rewrite pass panicked")
		}
	}()
	out = md
	for _, p := range append([]BodyRewriter{core}, passList(rest)...) {
		if out, err = p.Rewrite(out, s); err != nil {
			return "", err
		}
	}
	return out, nil
}

func passList(r []regPass) []BodyRewriter {
	out := make([]BodyRewriter, len(r))
	for i, x := range r {
		out[i] = x.p
	}
	return out
}
