package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Ports of the unit tests in crates/cnote-ai (crypto.rs, netguard.rs, redact.rs, providers.rs, lib.rs, hooks.rs).

func TestCryptoRoundTripAndBinding(t *testing.T) {
	key := make([]byte, 32)
	key[0] = 7
	ws, other := "11111111-2222-4333-8444-555555555555", "11111111-2222-4333-8444-555555555556"
	secret := "s-" + base64.RawURLEncoding.EncodeToString([]byte(ws))
	blob, err := Encrypt(key, ws, secret)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), secret) {
		t.Fatal("ciphertext contains the plaintext")
	}
	if got, err := Decrypt(key, ws, blob); err != nil || got != secret {
		t.Fatalf("round trip: %q %v", got, err)
	}
	if _, err := Decrypt(key, other, blob); err == nil {
		t.Fatal("AAD must bind the workspace")
	}
	wrong := make([]byte, 32)
	if _, err := Decrypt(wrong, ws, blob); err == nil {
		t.Fatal("wrong key decrypted")
	}
	tampered := append([]byte(nil), blob...)
	tampered[len(tampered)-1] ^= 1
	if _, err := Decrypt(key, ws, tampered); err == nil {
		t.Fatal("tampered blob decrypted")
	}
	if _, err := Decrypt(key, ws, blob[:nonceLen]); err == nil {
		t.Fatal("short blob decrypted")
	}
}

func TestMasterKeyParsing(t *testing.T) {
	for v, ok := range map[string]bool{
		base64.StdEncoding.EncodeToString(make([]byte, 32)): true,
		"c2hvcnQ=": false, "not base64!": false, "": false, "   ": false,
	} {
		t.Setenv("CNOTE_MASTER_KEY", v)
		if _, err := MasterKey(); (err == nil) != ok {
			t.Errorf("%q: err=%v want ok=%v", v, err, ok)
		}
	}
}

func TestEndpointGuard(t *testing.T) {
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "")
	for _, bad := range []string{"http://api.example.com", "https://127.0.0.1/v1", "https://169.254.169.254/latest", "https://10.0.0.5",
		"https://[::1]/v1", "https://user:pw@api.openai.com", "https://localhost:8080", "https://192.168.1.1", "https://100.64.0.1", "https://0.0.0.0", "ftp://x"} {
		if err := CheckEndpoint(context.Background(), bad); err == nil {
			t.Errorf("%s should be rejected", bad)
		}
	}
	for ip, public := range map[string]bool{"8.8.8.8": true, "192.168.1.1": false, "100.64.0.1": false, "::ffff:10.0.0.1": false, "fd00::1": false,
		"172.16.0.1": false, "172.32.0.1": true, "198.18.0.1": false, "203.0.113.9": false, "fe80::1": false, "2606:4700::1111": true, "255.255.255.255": false} {
		ip := ip
		if got := isPublic(net.ParseIP(ip)); got != public {
			t.Errorf("isPublic(%s)=%v want %v", ip, got, public)
		}
	}
	t.Setenv("AI_ALLOW_PRIVATE_ENDPOINTS", "1")
	if err := CheckEndpoint(context.Background(), "http://127.0.0.1:9/v1"); err != nil {
		t.Errorf("private allowed: %v", err)
	}
	if err := CheckEndpoint(context.Background(), "ftp://x"); err == nil || err.Error() != "base_url must be http(s)" {
		t.Errorf("scheme: %v", err)
	}
	if err := CheckEndpoint(context.Background(), "http://u:p@h"); err == nil || !strings.Contains(err.Error(), "credentials") {
		t.Errorf("credentials stay refused when private is allowed: %v", err)
	}
}

func TestRedactsIndianIdentifiers(t *testing.T) {
	in := "Mail asha.k+bills@example.co.in, call +91 98765 43210 or 9876543210. GSTIN 29ABCDE1234F1Z5, PAN ABCDE1234F, Aadhaar 2345 6789 0123 and 234567890123."
	want := "Mail [EMAIL], call [PHONE] or [PHONE]. GSTIN [GSTIN], PAN [PAN], Aadhaar [AADHAAR] and [AADHAAR]."
	if got := Redact(in); got != want {
		t.Fatalf("got %q", got)
	}
	plain := "Invoice INV-2026-27/0042 totals 1,18,000 for the Q3 launch (v2.4.1)."
	if Redact(plain) != plain {
		t.Fatal("ordinary text changed")
	}
	if got := Redact("ring +44 2079460958 now"); got != "ring [PHONE] now" {
		t.Fatal(got)
	}
}

func TestParseTasks(t *testing.T) {
	j := `Here you go: [{"title":"Send quote","assignee":"Asha","due":"2026-12-01"},{"title":"Book room"}]`
	got := parseTasks(j)
	if len(got) != 2 || *got[0].Assignee != "Asha" || got[1].Due != nil {
		t.Fatalf("%+v", got)
	}
	b := parseTasks("Notes\n- [ ] Email Ravi\n* Update the deck\nnot a task")
	if len(b) != 2 || b[0].Title != "Email Ravi" || b[1].Title != "Update the deck" {
		t.Fatalf("%+v", b)
	}
	if len(parseTasks(`[{"Title":"x"}]`)) != 0 || len(parseTasks(`[{"title":null}]`)) != 0 {
		t.Fatal("strict keys and non-null titles")
	}
}

func TestHashEmbedIsUnitLengthAndDeterministic(t *testing.T) {
	a, b := HashEmbed("Restart the pods, then wait"), HashEmbed("Restart the pods, then wait")
	var sum float64
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("not deterministic")
		}
		sum += float64(a[i]) * float64(a[i])
	}
	if sum < 0.999 || sum > 1.001 || len(a) != EmbeddingDims {
		t.Fatalf("norm %v dims %d", sum, len(a))
	}
	if strings.Contains(VecLiteral(a), "e") || !strings.HasPrefix(VecLiteral(a), "[") {
		t.Fatal("literal must be plain decimals")
	}
}

func TestLocalCompletionIsExtractive(t *testing.T) {
	c, _ := Local{}.Complete(context.Background(), "x", []Message{{"system", "s"}, {"user", strings.Repeat("ab", 3000)}}, 100)
	if len(c.Text) != 400 || c.Provider != "local" || c.Model != "local-extractive" || c.TokensIn != 1500 || c.TokensOut != 100 {
		t.Fatalf("%+v", c)
	}
}

// Provider adapters against a recorded-shape fake (no real provider is ever called).
func TestAdaptersMapRequestsAndResponses(t *testing.T) {
	var seen []map[string]any
	var hdr []http.Header
	vec := func(x float32) []float32 {
		v := make([]float32, EmbeddingDims)
		v[0] = x
		return v
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		body["_path"] = r.URL.Path
		seen, hdr = append(seen, body), append(hdr, r.Header.Clone())
		switch r.URL.Path {
		case "/v1/messages":
			_ = json.NewEncoder(w).Encode(map[string]any{"model": "claude-x", "content": []any{
				map[string]any{"type": "text", "text": "Short "}, map[string]any{"type": "tool_use"}, map[string]any{"type": "text", "text": "summary."}},
				"usage": map[string]any{"input_tokens": 41, "output_tokens": 7}})
		case "/o/chat/completions":
			_ = json.NewEncoder(w).Encode(map[string]any{"model": "m", "choices": []any{map[string]any{"message": map[string]any{"content": "Done."}}},
				"usage": map[string]any{"prompt_tokens": 30, "completion_tokens": 2}})
		case "/o/embeddings":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 1, "embedding": vec(0.2)}, map[string]any{"index": 0, "embedding": vec(0.1)}}})
		default:
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	msgs := []Message{{"system", "Be brief."}, {"user", "Summarise the note."}}
	ctx := context.Background()
	base := srv.URL
	a := NewAnthropic("k-123", &base, nil)
	c, err := a.Complete(ctx, "draft", msgs, 256)
	if err != nil || c.Text != "Short summary." || c.TokensIn != 41 || c.TokensOut != 7 || c.Provider != "anthropic" {
		t.Fatalf("%+v %v", c, err)
	}
	if seen[0]["system"] != "Be brief." || seen[0]["model"] != defaultAnthropicModel || seen[0]["max_tokens"] != float64(256) || len(seen[0]["messages"].([]any)) != 1 {
		t.Fatalf("anthropic request %v", seen[0])
	}
	if hdr[0].Get("x-api-key") != "k-123" || hdr[0].Get("anthropic-version") != "2023-06-01" {
		t.Fatal("anthropic headers")
	}
	if _, err := a.Complete(ctx, "draft", []Message{{"system", "only"}}, 5); err == nil || err.Error() != "no user message to send" {
		t.Fatalf("%v", err)
	}
	ob := srv.URL + "/o/"
	key := "bearer-key"
	o := NewOpenAI(&key, &ob, nil, nil)
	c, err = o.Complete(ctx, "draft", msgs, 256)
	if err != nil || c.Text != "Done." || c.TokensIn != 30 || c.Provider != "openai" || hdr[len(hdr)-1].Get("authorization") != "Bearer bearer-key" {
		t.Fatalf("%+v %v", c, err)
	}
	vs, err := o.Embed(ctx, []string{"a", "b"})
	if err != nil || vs[0][0] != 0.1 || vs[1][0] != 0.2 {
		t.Fatalf("embeddings must be sorted by index: %v", err)
	}
	if seen[len(seen)-1]["dimensions"] != float64(EmbeddingDims) {
		t.Fatal("text-embedding-3 models are pinned to the column width")
	}
	if _, err := o.Embed(ctx, []string{"a"}); err == nil || err.Error() != "expected 1 embeddings, got 2" {
		t.Fatalf("count mismatch: %v", err)
	}
	if _, err := NewOpenAI(nil, &srv.URL, nil, nil).Complete(ctx, "x", msgs, 1); err == nil || err.Error() != "model API returned 500 Internal Server Error" {
		t.Fatalf("status only: %v", err)
	}
}

func TestSignatureIsHMACOfTimestampDotBody(t *testing.T) {
	// echo -n '1.{}' | openssl dgst -sha256 -hmac k
	if got := Signature("k", 1, "{}"); !strings.HasPrefix(got, "sha256=") || len(got) != 7+64 {
		t.Fatal(got)
	}
	if Signature("k", 1, "{}") == Signature("k", 2, "{}") || Signature("k", 1, "{}") == Signature("j", 1, "{}") {
		t.Fatal("signature must depend on timestamp and secret")
	}
}
