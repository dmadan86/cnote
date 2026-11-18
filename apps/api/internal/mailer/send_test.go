package mailer

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
)

type sink struct {
	mu         sync.Mutex
	from, data string
}

func startSink(t *testing.T, eightBit bool) *sink {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &sink{}
	t.Cleanup(func() { ln.Close() })
	t.Setenv("SMTP_URL", "smtp://"+ln.Addr().String())
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r, w := bufio.NewReader(c), func(l string) { fmt.Fprintf(c, "%s\r\n", l) }
				w("220 sink")
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					switch cmd := strings.ToUpper(strings.TrimSpace(line)); {
					case strings.HasPrefix(cmd, "EHLO"):
						if eightBit {
							w("250-sink")
							w("250 8BITMIME")
						} else {
							w("250 sink")
						}
					case strings.HasPrefix(cmd, "MAIL FROM:"):
						s.mu.Lock()
						s.from = strings.TrimSpace(line[len("MAIL FROM:"):])
						s.mu.Unlock()
						w("250 ok")
					case cmd == "DATA":
						w("354 go")
						var b strings.Builder
						for {
							l, err := r.ReadString('\n')
							if err != nil || l == ".\r\n" {
								break
							}
							b.WriteString(l)
						}
						s.mu.Lock()
						s.data = b.String()
						s.mu.Unlock()
						w("250 queued")
					case cmd == "QUIT":
						w("221 bye")
						return
					default:
						w("250 ok")
					}
				}
			}()
		}
	}()
	return s
}

func TestSendHeadersAndParts(t *testing.T) {
	s := startSink(t, true)
	t.Setenv("EMAIL_FROM", "C Note Billing <billing@example.test>")
	id, err := Send(context.Background(), Mail{To: "cust@example.test", ReplyTo: "Acme <help@acme.test>", FromName: "Acme Traders",
		Subject: "Invoice ₹1,18,000\r\nBcc: x@y.test", Text: "नमस्ते ₹1,18,000", HTML: "<p>नमस्ते</p>"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s.from, "<billing@example.test>") {
		t.Fatalf("envelope sender %q", s.from)
	}
	d := s.data
	head := strings.SplitN(d, "\r\n\r\n", 2)[0]
	for _, w := range []string{`From: "Acme Traders via C Note Billing" <billing@example.test>`, "To: cust@example.test", `Reply-To: "Acme" <help@acme.test>`,
		"Date: ", "Message-ID: " + id, "multipart/alternative", "Subject: =?utf-8?q?"} {
		if !strings.Contains(head, w) {
			t.Errorf("%q missing in headers %q", w, head)
		}
	}
	if !strings.HasSuffix(strings.TrimSuffix(id, ">"), "@example.test") || strings.Contains(head, "\nBcc:") {
		t.Errorf("id %q or injected header in %q", id, head)
	}
	if i, j := strings.Index(d, "text/plain"), strings.Index(d, "text/html"); i < 0 || j < i || !strings.Contains(d, "Content-Transfer-Encoding: 8bit\r\n\r\nनमस्ते ₹1,18,000") {
		t.Errorf("text part must come first and be readable:\n%s", d)
	}
}

func TestSendQuotedPrintableWithout8BitMIME(t *testing.T) {
	s := startSink(t, false)
	t.Setenv("EMAIL_FROM", "billing@example.test")
	if _, err := Send(context.Background(), Mail{To: "cust@example.test", FromName: "Acme", Subject: "Hi", Text: "₹1"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s.data, "quoted-printable") || !strings.Contains(s.data, "From: \"Acme via C Note\" <billing@example.test>") || strings.Contains(s.data, "multipart") {
		t.Fatalf("%s", s.data)
	}
}

func TestSendRejects(t *testing.T) {
	startSink(t, false)
	t.Setenv("EMAIL_FROM", "billing@example.test")
	for _, m := range []Mail{{To: "a@b.test\r\nBcc: x@y.test", Text: "x"}, {To: "a@b.test", ReplyTo: "a@b.test, c@d.test", Text: "x"}, {To: "nope", Text: "x"}} {
		if _, err := Send(context.Background(), m); err == nil {
			t.Errorf("accepted %+v", m)
		}
	}
	t.Setenv("EMAIL_FROM", "")
	if _, err := Send(context.Background(), Mail{To: "a@b.test", Text: "x"}); err != ErrNotConfigured {
		t.Errorf("got %v", err)
	}
}
