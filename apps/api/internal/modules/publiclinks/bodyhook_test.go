package publiclinks

import (
	"errors"
	"strings"
	"testing"
)

type fakePass struct {
	name string
	fn   func(string) (string, error)
}

func (f fakePass) Name() string { return f.name }
func (f fakePass) Rewrite(md string, _ BodyScope) (string, error) {
	return f.fn(md)
}

// reset gives a test its own registry; the real passes register from init() in later packages.
func resetBodyPasses(t *testing.T) {
	t.Helper()
	bodyMu.Lock()
	oc, op := bodyCore, bodyPasses
	bodyCore, bodyPasses = nil, nil
	bodyMu.Unlock()
	t.Cleanup(func() { bodyMu.Lock(); bodyCore, bodyPasses = oc, op; bodyMu.Unlock() })
}

func TestPublicBodyFailsClosedWithoutCore(t *testing.T) {
	resetBodyPasses(t)
	if _, err := PublicBody("secret [@Ravi](/w/x/people/y)", BodyScope{}); !errors.Is(err, ErrNoCoreBodyPass) {
		t.Fatalf("want ErrNoCoreBodyPass, got %v", err)
	}
	RegisterBodyPass(0, fakePass{"extra", func(s string) (string, error) { return s, nil }})
	if _, err := PublicBody("x", BodyScope{}); !errors.Is(err, ErrNoCoreBodyPass) {
		t.Fatal("an extra pass alone must not serve a body")
	}
}

func TestPublicBodyOrderIsCoreThenOrderThenName(t *testing.T) {
	resetBodyPasses(t)
	add := func(tag string) func(string) (string, error) {
		return func(s string) (string, error) { return s + tag, nil }
	}
	RegisterBodyPass(20, fakePass{"b", add("B")})
	RegisterBodyPass(10, fakePass{"z", add("Z")})
	RegisterBodyPass(10, fakePass{"a", add("A")})
	RegisterCoreBodyPass(fakePass{"core", add("C")})
	got, err := PublicBody(">", BodyScope{})
	if err != nil || got != ">CAZB" {
		t.Fatalf("got %q %v", got, err)
	}
}

func TestPublicBodyErrorAndPanicNeverReturnTheInput(t *testing.T) {
	resetBodyPasses(t)
	RegisterCoreBodyPass(fakePass{"core", func(s string) (string, error) { return s, nil }})
	RegisterBodyPass(1, fakePass{"bad", func(string) (string, error) { return "", errors.New("boom") }})
	if out, err := PublicBody("secret", BodyScope{}); err == nil || out != "" {
		t.Fatalf("an error must give no body: %q %v", out, err)
	}
	resetBodyPasses(t)
	RegisterCoreBodyPass(fakePass{"core", func(s string) (string, error) { panic("x") }})
	if out, err := PublicBody("secret", BodyScope{}); err == nil || strings.Contains(out, "secret") {
		t.Fatalf("a panic must give no body: %q %v", out, err)
	}
}

func TestCoreBodyPassRegisteredTwicePanics(t *testing.T) {
	resetBodyPasses(t)
	RegisterCoreBodyPass(fakePass{"one", nil})
	defer func() {
		if recover() == nil {
			t.Fatal("a second core must panic")
		}
	}()
	RegisterCoreBodyPass(fakePass{"two", nil})
}
