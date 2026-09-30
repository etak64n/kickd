//go:build e2e && windows

package windows

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// The webhook server of testdata/webhook listens on port 18787, so these
// tests run one at a time.

// webhook returns a home with the config of testdata/webhook, and its
// agent running.
func webhook(t *testing.T) *home {
	t.Helper()
	h := newHome(t, "webhook")
	h.start()
	return h
}

// sign returns the signature of body with the secret, as GitHub sends it.
func sign(body string) string {
	mac := hmac.New(sha256.New, []byte("webhook-secret"))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// answer is the answer of a webhook trigger with wait: true.
type answer struct {
	ExitCode int
	Output   string
}

func decode(t *testing.T, body string) answer {
	t.Helper()
	var r answer
	if err := json.Unmarshal([]byte(body), &r); err != nil {
		t.Fatalf("%s: %v", body, err)
	}
	return r
}

func TestWebhookAcceptsARequestSignedWithTheSecret(t *testing.T) {
	h := webhook(t)
	body := `{"ref":"main"}`
	if res := request(t, "POST", 18787, "/hooks/deploy", body, "X-Hub-Signature-256", sign(body)); res.status != http.StatusAccepted {
		t.Fatalf("a signed request: %d %s", res.status, res.body)
	}
	r := h.waitForRuns("deploy", 1)[0]
	if r.line("trigger") != "webhook" || !strings.Contains(r.line("body"), `"body":"{\"ref\":\"main\"}"`) {
		t.Errorf("deploy:\n%s", r.Output)
	}
}

func TestWebhookRefusesARequestWithAWrongSignature(t *testing.T) {
	h := webhook(t)
	body := `{"ref":"main"}`
	if res := request(t, "POST", 18787, "/hooks/deploy", body, "X-Hub-Signature-256", sign("another body")); res.status != http.StatusUnauthorized {
		t.Fatalf("a wrongly signed request: %d %s", res.status, res.body)
	}
	if rs := h.runs("deploy"); len(rs) != 0 {
		t.Errorf("deploy ran: %v", rs)
	}
}

func TestWebhookRefusesARequestWithoutASignature(t *testing.T) {
	h := webhook(t)
	if res := request(t, "POST", 18787, "/hooks/deploy", `{"ref":"main"}`); res.status != http.StatusUnauthorized {
		t.Fatalf("an unsigned request: %d %s", res.status, res.body)
	}
	if rs := h.runs("deploy"); len(rs) != 0 {
		t.Errorf("deploy ran: %v", rs)
	}
}

func TestWebhookRefusesARequestWithoutTheToken(t *testing.T) {
	webhook(t)
	if res := request(t, "POST", 18787, "/hooks/build", ""); res.status != http.StatusUnauthorized {
		t.Errorf("a request without the token: %d %s", res.status, res.body)
	}
}

func TestWebhookRefusesARequestWithAWrongToken(t *testing.T) {
	webhook(t)
	if res := request(t, "POST", 18787, "/hooks/build", "", "Authorization", "Bearer another-token"); res.status != http.StatusUnauthorized {
		t.Errorf("a request with a wrong token: %d %s", res.status, res.body)
	}
}

func TestWebhookWaitAnswersWithTheOutputOfTheRun(t *testing.T) {
	webhook(t)
	res := request(t, "POST", 18787, "/hooks/build", "", "Authorization", "Bearer webhook-token")
	if r := decode(t, res.body); res.status != http.StatusOK || r.ExitCode != 0 || !strings.Contains(r.Output, "building main") {
		t.Errorf("build: %d %s", res.status, res.body)
	}
}

func TestWebhookWaitAnswersWithTheFailureOfTheRun(t *testing.T) {
	webhook(t)
	res := request(t, "POST", 18787, "/hooks/test", "", "Authorization", "Bearer webhook-token")
	if r := decode(t, res.body); res.status != http.StatusInternalServerError || r.ExitCode != 3 || !strings.Contains(r.Output, "2 of 10 tests failed") {
		t.Errorf("test: %d %s", res.status, res.body)
	}
}

func TestWebhookTakesAParameterFromTheQuery(t *testing.T) {
	webhook(t)
	res := request(t, "POST", 18787, "/hooks/build?ref=v1.2", "", "Authorization", "Bearer webhook-token")
	if r := decode(t, res.body); res.status != http.StatusOK || !strings.Contains(r.Output, "building v1.2") {
		t.Errorf("build?ref=v1.2: %d %s", res.status, res.body)
	}
}

func TestWebhookRefusesAMethodThatIsNotListed(t *testing.T) {
	webhook(t)
	if res := request(t, "GET", 18787, "/hooks/build", "", "Authorization", "Bearer webhook-token"); res.status != http.StatusMethodNotAllowed {
		t.Errorf("GET /hooks/build: %d %s", res.status, res.body)
	}
}

func TestWebhookKeepsTheRequestIDOfTheCaller(t *testing.T) {
	h := webhook(t)
	body := `{}`
	res := request(t, "POST", 18787, "/hooks/deploy", body, "X-Hub-Signature-256", sign(body), "X-Request-ID", "delivery-42")
	if res.header.Get("X-Request-ID") != "delivery-42" {
		t.Errorf("the response carries the request ID %q", res.header.Get("X-Request-ID"))
	}
	if r := h.waitForRuns("deploy", 1)[0]; r.RequestID != "delivery-42" || r.line("request") != "delivery-42" {
		t.Errorf("the run has the request ID %q, and the command got %q", r.RequestID, r.line("request"))
	}
}

func TestWebhookRefusesABodyOverMaxBodyBytes(t *testing.T) {
	webhook(t)
	body := strings.Repeat("x", 2048)
	if res := request(t, "POST", 18787, "/hooks/build", body, "Authorization", "Bearer webhook-token"); res.status != http.StatusRequestEntityTooLarge {
		t.Errorf("a body of 2 KB: %d %s", res.status, res.body)
	}
}
