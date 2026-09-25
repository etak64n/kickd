//go:build usecase

package usecase

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // the zones of the cron test, also on Windows
)

// noop is a command that succeeds at once: a builtin of the shell.
func noop() string {
	if runtime.GOOS == "windows" {
		return "'exit 0'"
	}
	return "'true'"
}

// addEvents appends events, written in YAML, to the config of the home.
func (h *home) addEvents(yaml string) {
	h.t.Helper()
	b, err := os.ReadFile(h.cfg)
	if err != nil {
		h.t.Fatal(err)
	}
	h.write(string(b) + yaml)
	h.must("check")
}

// payload is the part of the payload of a run that the trigger tests read.
type payload struct {
	Data  map[string]string `json:"data"`
	Files []struct {
		Path string `json:"path"`
		Op   string `json:"op"`
	} `json:"files"`
	Cron *struct {
		ScheduledAt string `json:"scheduledAt"`
		Missed      bool   `json:"missed"`
	} `json:"cron"`
	Webhook *struct {
		Method string `json:"method"`
		Body   string `json:"body"`
	} `json:"webhook"`
}

// payloadOf returns the payload of the run with the ID id, as kickd show
// prints it.
func (h *home) payloadOf(id int64) payload {
	h.t.Helper()
	var v struct{ Payload payload }
	if err := json.Unmarshal([]byte(h.must("show", strconv.FormatInt(id, 10), "--json")), &v); err != nil {
		h.t.Fatal(err)
	}
	return v.Payload
}

// waitLogCount waits for n records with the message msg in the log file
// after offset.
func (h *home) waitLogCount(offset int64, msg string, n int, timeout time.Duration) {
	h.t.Helper()
	waitFor(h.t, fmt.Sprintf("%d log records %q", n, msg), timeout, func() bool {
		b, err := os.ReadFile(h.log)
		if err != nil || int64(len(b)) < offset {
			return false
		}
		count := 0
		for _, line := range strings.Split(string(b[offset:]), "\n") {
			var rec struct{ Message string }
			if json.Unmarshal([]byte(line), &rec) == nil && rec.Message == msg {
				count++
			}
		}
		return count >= n
	})
}

// request sends an HTTP request to the webhook server of the home, with
// headers given as name and value, and returns the status, the body and
// the headers of the response.
func (h *home) request(method, path, body string, headers ...string) (int, string, http.Header) {
	h.t.Helper()
	req, err := http.NewRequest(method, "http://127.0.0.1:"+h.port+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b), res.Header
}

// change is one file change of a firing: its kind and its path.
type change struct{ op, path string }

// changesSince returns the changes of the runs of event after the run
// with the ID after, and the ID of the last of those runs.
func (h *home) changesSince(event string, after int64) ([]change, int64) {
	h.t.Helper()
	var out []change
	last := after
	for _, r := range h.runs(event) {
		if r.ID <= after {
			continue
		}
		for _, c := range h.payloadOf(r.ID).Files {
			out = append(out, change{c.Op, c.Path})
		}
		last = max(last, r.ID)
	}
	return out, last
}

// has reports whether the changes hold a change of the kind op to path.
func has(changes []change, op, path string) bool {
	for _, c := range changes {
		if c.op == op && samePath(c.path, path) {
			return true
		}
	}
	return false
}

// File changes of every kind fire the event with the changes in the
// payload, a directory created later is watched as well, and include,
// exclude, changes and recursive choose which changes count.
func TestUseCaseFileChanges(t *testing.T) {
	t.Parallel()
	h := setup(t)
	w := filepath.Join(h.dir, "watched")
	all, filtered, creates, top := filepath.Join(w, "all"), filepath.Join(w, "filtered"), filepath.Join(w, "creates"), filepath.Join(w, "top")
	for _, d := range []string{all, filtered, creates, filepath.Join(top, "sub")} {
		mkdir(t, d)
	}
	event := func(name, path, keys string) string {
		return "\n  - name: " + name + "\n    command: " + noop() + "\n    concurrency: queue\n    triggers:\n      - type: file\n        path: '" + path + "'\n" + keys
	}
	h.addEvents(event("watch-all", all, "        recursive: true\n        debounce: 500ms\n") +
		event("watch-filtered", filtered, "        include: ['*.txt']\n        exclude: ['skip*']\n        debounce: 300ms\n") +
		event("watch-creates", creates, "        changes: [create]\n        debounce: 300ms\n") +
		event("watch-top", top, "        debounce: 300ms\n"))
	offset := fileSize(h.log)
	h.start()
	// The build event of the README and the four events here.
	h.waitLogCount(offset, "File watch started", 5, 30*time.Second)

	seen := map[string]int64{}
	// expect waits until the runs of event after the last one seen hold
	// every wanted change, given as kind and path.
	expect := func(what, event string, want ...string) {
		t.Helper()
		var got []change
		waitFor(t, what, 30*time.Second, func() bool {
			var last int64
			got, last = h.changesSince(event, seen[event])
			for i := 0; i+1 < len(want); i += 2 {
				if !has(got, want[i], want[i+1]) {
					return false
				}
			}
			seen[event] = last
			return true
		}, func() string { return fmt.Sprintf("the changes of %s are %v", event, got) })
	}
	a, b := filepath.Join(all, "a.txt"), filepath.Join(all, "b.txt")
	writeText(t, a, "1\n")
	expect("creating a file fires watch-all", "watch-all", "create", a)
	f, err := os.OpenFile(a, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("2\n")
	f.Close()
	expect("writing a file fires watch-all", "watch-all", "write", a)
	if err := os.Rename(a, b); err != nil {
		t.Fatal(err)
	}
	expect("renaming a file fires watch-all", "watch-all", "rename", a)
	if err := os.Remove(b); err != nil {
		t.Fatal(err)
	}
	expect("removing a file fires watch-all", "watch-all", "remove", b)
	sub := filepath.Join(all, "sub")
	mkdir(t, sub)
	expect("creating a directory fires watch-all", "watch-all", "create", sub)
	c := filepath.Join(sub, "c.txt")
	writeText(t, c, "3\n")
	expect("a file in the new directory fires watch-all", "watch-all", "create", c)

	// include and exclude: only keep.txt counts.
	writeText(t, filepath.Join(filtered, "x.log"), "")
	writeText(t, filepath.Join(filtered, "skip.txt"), "")
	keep := filepath.Join(filtered, "keep.txt")
	writeText(t, keep, "")
	expect("an included file fires watch-filtered", "watch-filtered", "create", keep)

	// changes: [create]: a write and a removal do not count.
	created := filepath.Join(creates, "new.txt")
	writeText(t, created, "")
	expect("creating a file fires watch-creates", "watch-creates", "create", created)
	writeText(t, created, "more\n")
	if err := os.Remove(created); err != nil {
		t.Fatal(err)
	}

	// Without recursive, a file in a subdirectory does not count.
	deep, shallow := filepath.Join(top, "sub", "deep.txt"), filepath.Join(top, "shallow.txt")
	writeText(t, deep, "")
	writeText(t, shallow, "")
	expect("a file at the top fires watch-top", "watch-top", "create", shallow)

	// Changes that do not count would have fired by now.
	time.Sleep(2 * time.Second)
	for _, e := range []struct {
		event string
		bad   func(change) bool
	}{
		{"watch-filtered", func(c change) bool { return !samePath(c.path, keep) }},
		{"watch-creates", func(c change) bool { return c.op != "create" }},
		{"watch-top", func(c change) bool { return samePath(c.path, deep) }},
	} {
		got, _ := h.changesSince(e.event, 0)
		if slices.ContainsFunc(got, e.bad) {
			t.Errorf("%s fired for changes that do not count: %v", e.event, got)
		}
	}
	h.stop()
}

// A cron trigger fires at its scheduled time in its time zone: zones with
// offsets of whole hours, half an hour and three quarters of an hour, and
// one with daylight saving time.
func TestUseCaseCronTimeZones(t *testing.T) {
	t.Parallel()
	h := setup(t)
	at := time.Now().UTC().Truncate(time.Second).Add(25 * time.Second)
	zones := map[string]string{
		"cron-utc": "UTC", "cron-tokyo": "Asia/Tokyo", "cron-kolkata": "Asia/Kolkata",
		"cron-kathmandu": "Asia/Kathmandu", "cron-st-johns": "America/St_Johns",
	}
	var added strings.Builder
	for name, zone := range zones {
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatal(err)
		}
		lt := at.In(loc)
		added.WriteString(fmt.Sprintf("\n  - name: %s\n    command: %s\n    triggers:\n      - type: cron\n        schedule: '%d %d %d * * *'\n        timezone: %s\n",
			name, noop(), lt.Second(), lt.Minute(), lt.Hour(), zone))
	}
	h.addEvents(added.String())
	h.start()
	if !time.Now().Before(at) {
		t.Fatalf("the agent started after the scheduled time %s", at)
	}
	for name, zone := range zones {
		rs := h.waitRuns(name, zone+" fires", time.Until(at)+30*time.Second, func(rs []record) bool {
			return len(rs) > 0 && final(rs[0])
		})
		r, p := rs[0], h.payloadOf(rs[0].ID)
		if r.Trigger != "cron" || p.Cron == nil || !parseTime(p.Cron.ScheduledAt).Equal(at) || p.Cron.Missed {
			t.Errorf("%s: %v, payload %+v, want the time %s", zone, r, p.Cron, at)
		}
		if d := r.started().Sub(at); d < 0 || d > 5*time.Second {
			t.Errorf("%s: the run started %s after the scheduled time", zone, d)
		}
	}
	h.stop()
}

// A scheduled time that passes while kickd is stopped runs once when kickd
// starts again with missed: run, and does not run with missed: skip.
func TestUseCaseCronMissed(t *testing.T) {
	t.Parallel()
	h := setup(t)
	at := time.Now().UTC().Truncate(time.Second).Add(25 * time.Second)
	schedule := fmt.Sprintf("%d %d %d * * *", at.Second(), at.Minute(), at.Hour())
	h.addEvents("\n  - name: missed-run\n    command: " + noop() + "\n    triggers:\n      - type: cron\n        schedule: '" + schedule + "'\n        timezone: UTC\n        missed: run\n" +
		"\n  - name: missed-skip\n    command: " + noop() + "\n    triggers:\n      - type: cron\n        schedule: '" + schedule + "'\n        timezone: UTC\n        missed: skip\n")
	h.start()
	// The agent records when it started to look at the schedules.
	time.Sleep(2 * time.Second)
	h.stop()
	if !time.Now().Before(at) {
		t.Fatalf("the agent stopped after the scheduled time %s", at)
	}
	// A scheduled time counts as missed when kickd notices it more than a
	// minute late.
	time.Sleep(time.Until(at.Add(65 * time.Second)))
	offset := fileSize(h.log)
	h.start()
	rs := h.waitRuns("missed-run", "the missed time runs", 30*time.Second, func(rs []record) bool {
		return len(rs) > 0 && final(rs[0])
	})
	if p := h.payloadOf(rs[0].ID); len(rs) != 1 || rs[0].Trigger != "cron" || p.Cron == nil || !p.Cron.Missed || !parseTime(p.Cron.ScheduledAt).Equal(at) {
		t.Errorf("missed-run: %v, payload %+v, want one run for %s", rs, p.Cron, at)
	}
	h.waitLog(offset, "Missed schedule caught up", 10*time.Second)
	h.waitLog(offset, "Missed schedule skipped", 10*time.Second)
	if rs := h.runs("missed-skip"); len(rs) != 0 {
		t.Errorf("missed-skip ran: %v", rs)
	}
	h.stop()
}

// The keys of webhook triggers: a signature with the secret, parameters
// from the query, wait: true with the result in the response, methods,
// and the request ID of the caller.
func TestUseCaseWebhookFeatures(t *testing.T) {
	t.Parallel()
	h := setup(t)
	echo := "'echo waited-$KICKD_DATA_REF'"
	if runtime.GOOS == "windows" {
		echo = "'echo waited-%KICKD_DATA_REF%'"
	}
	h.addEvents(`
  - name: signed
    command: ` + noop() + `
    triggers:
      - type: webhook
        path: '/hooks/signed'
        secret: 'test-secret'
  - name: waited
    command: ` + echo + `
    params:
      - name: ref
        default: main
    triggers:
      - type: webhook
        path: '/hooks/waited'
        token: '` + token + `'
        wait: true
  - name: failing
    command: 'exit 3'
    triggers:
      - type: webhook
        path: '/hooks/failing'
        token: '` + token + `'
        wait: true
`)
	h.start()
	auth := []string{"Authorization", "Bearer " + token}

	// secret: the body signed with HMAC-SHA256, as GitHub sends it.
	body := `{"ref":"main"}`
	mac := hmac.New(sha256.New, []byte("test-secret"))
	mac.Write([]byte(body))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if code, _, _ := h.request("POST", "/hooks/signed", body); code != http.StatusUnauthorized {
		t.Errorf("a request without a signature got %d, want 401", code)
	}
	if code, _, _ := h.request("POST", "/hooks/signed", body, "X-Hub-Signature-256", "sha256=00"); code != http.StatusUnauthorized {
		t.Errorf("a request with a wrong signature got %d, want 401", code)
	}
	code, _, hdr := h.request("POST", "/hooks/signed", body, "X-Hub-Signature-256", sig, "X-Request-ID", "usecase-signed-1")
	if code != http.StatusAccepted || hdr.Get("X-Request-ID") != "usecase-signed-1" {
		t.Errorf("a signed request got %d, request ID %q", code, hdr.Get("X-Request-ID"))
	}
	rs := h.waitRuns("signed", "a signed request fires signed", 30*time.Second, func(rs []record) bool { return len(rs) == 1 && final(rs[0]) })
	if p := h.payloadOf(rs[0].ID); rs[0].RequestID != "usecase-signed-1" || p.Webhook == nil || p.Webhook.Body != body {
		t.Errorf("signed: %v, request %q, payload %+v", rs[0], rs[0].RequestID, p.Webhook)
	}

	// wait: true answers with the result; ref comes from the query.
	var result struct {
		ExitCode int
		Output   string
	}
	for _, c := range []struct{ query, want string }{{"?ref=v1.2&other=x", "waited-v1.2"}, {"", "waited-main"}} {
		code, resp, _ := h.request("POST", "/hooks/waited"+c.query, "", auth...)
		if err := json.Unmarshal([]byte(resp), &result); err != nil || code != http.StatusOK || result.ExitCode != 0 || !strings.Contains(result.Output, c.want) {
			t.Errorf("waited%s: %d %s", c.query, code, resp)
		}
	}
	rs = h.runs("waited")
	if len(rs) != 2 {
		t.Fatalf("waited ran %d times, want 2", len(rs))
	}
	if p := h.payloadOf(rs[0].ID); p.Data["ref"] != "v1.2" || p.Data["other"] != "" {
		t.Errorf("waited: the parameters are %v, want ref=v1.2 and no other", p.Data)
	}
	code, resp, _ := h.request("POST", "/hooks/failing", "", auth...)
	if err := json.Unmarshal([]byte(resp), &result); err != nil || code != http.StatusInternalServerError || result.ExitCode != 3 {
		t.Errorf("failing: %d %s", code, resp)
	}

	// methods: [POST] on deploy of the README.
	if code, _, _ := h.request("GET", "/hooks/deploy", "", auth...); code != http.StatusMethodNotAllowed {
		t.Errorf("GET /hooks/deploy got %d, want 405", code)
	}
	h.stop()
}
