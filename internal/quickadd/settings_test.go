package quickadd_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"beprod/internal/models"
	"beprod/internal/secure"
	"beprod/internal/testutil"
)

const urlPrefix = "http://app.test/q/"

func enable(t *testing.T, a *testutil.TestApp, cookie, pin string) testutil.Response {
	t.Helper()
	return a.Do(t, "POST", "/api/v1/quick-add/enable", map[string]string{"pin": pin, "pinConfirm": pin}, cookie)
}

func tokenOf(t *testing.T, r testutil.Response) string {
	t.Helper()
	u, _ := r.Body["url"].(string)
	if !strings.HasPrefix(u, urlPrefix) {
		t.Fatalf("url %q lacks prefix %q (status %d)", u, urlPrefix, r.Status)
	}
	return strings.TrimPrefix(u, urlPrefix)
}

func linkOf(t *testing.T, a *testutil.TestApp, userID uuid.UUID) models.QuickAddLink {
	t.Helper()
	var l models.QuickAddLink
	if err := a.DB.Where("user_id = ?", userID).First(&l).Error; err != nil {
		t.Fatal(err)
	}
	return l
}

func sessionCount(t *testing.T, a *testutil.TestApp, linkID uuid.UUID) int64 {
	t.Helper()
	var n int64
	a.DB.Model(&models.QuickAddSession{}).Where("link_id = ?", linkID).Count(&n)
	return n
}

func addSession(t *testing.T, a *testutil.TestApp, linkID uuid.UUID, exp time.Time) {
	t.Helper()
	s := models.QuickAddSession{LinkID: linkID, TokenHash: secure.HashToken("s1"), ExpiresAt: exp}
	if err := a.DB.Create(&s).Error; err != nil {
		t.Fatal(err)
	}
}

func TestSettings_RequireSession(t *testing.T) {
	a := testutil.NewApp(t)
	for _, c := range [][2]string{
		{"GET", "/api/v1/quick-add"}, {"POST", "/api/v1/quick-add/enable"}, {"PUT", "/api/v1/quick-add/pin"},
		{"POST", "/api/v1/quick-add/regenerate"}, {"POST", "/api/v1/quick-add/disable"},
	} {
		if r := a.Do(t, c[0], c[1], nil, ""); r.Status != 401 {
			t.Errorf("%s %s = %d, want 401", c[0], c[1], r.Status)
		}
	}
}

func TestEnable_PinMismatch(t *testing.T) {
	a := testutil.NewApp(t)
	u, ck := a.NewUser(t, "a@x.test")
	r := a.Do(t, "POST", "/api/v1/quick-add/enable", map[string]string{"pin": "1234", "pinConfirm": "1235"}, ck)
	if r.Status != 400 || r.Field("pinConfirm") != "PINs don't match" {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
	var n int64
	a.DB.Model(&models.QuickAddLink{}).Where("user_id = ? AND enabled = true", u.ID).Count(&n)
	if n != 0 {
		t.Fatalf("enabled links = %d", n)
	}
}

func TestEnable_InvalidPin(t *testing.T) {
	a := testutil.NewApp(t)
	u, ck := a.NewUser(t, "a@x.test")
	for _, pin := range []string{"123", "12345", "12ab", "abcd", ""} {
		r := enable(t, a, ck, pin)
		if r.Status != 400 || r.Field("pin") != "PIN must be exactly 4 digits" {
			t.Errorf("pin %q: got %d %s", pin, r.Status, r.Raw)
		}
	}
	var n int64
	a.DB.Model(&models.QuickAddLink{}).Where("user_id = ?", u.ID).Count(&n)
	if n != 0 {
		t.Fatalf("links = %d", n)
	}
}

func TestEnable_ReturnsURL(t *testing.T) {
	a := testutil.NewApp(t)
	u, ck := a.NewUser(t, "a@x.test")
	r := enable(t, a, ck, "1234")
	if r.Status != 200 {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
	tok := tokenOf(t, r)
	if len(tok) < 43 || !regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(tok) {
		t.Fatalf("bad token %q", tok)
	}
	l := linkOf(t, a, u.ID)
	if !l.Enabled || l.TokenHash != secure.HashToken(tok) {
		t.Fatalf("bad link %+v", l)
	}
}

func TestEnable_StoresOnlyHashes(t *testing.T) {
	a := testutil.NewApp(t)
	u, ck := a.NewUser(t, "a@x.test")
	tok := tokenOf(t, enable(t, a, ck, "1234"))
	l := linkOf(t, a, u.ID)
	if l.TokenHash == tok || len(l.TokenHash) != 64 {
		t.Errorf("token hash %q", l.TokenHash)
	}
	if !strings.HasPrefix(l.PinHash, "$2") || l.PinHash == "1234" {
		t.Errorf("pin hash %q", l.PinHash)
	}
	if !secure.CheckPassword(l.PinHash, "1234") || secure.CheckPassword(l.PinHash, "0000") {
		t.Error("pin hash check wrong")
	}
	m := a.DB.Migrator()
	if m.HasColumn(&models.QuickAddLink{}, "token") || m.HasColumn(&models.QuickAddLink{}, "pin") {
		t.Error("plain token/pin column exists")
	}
	var n int64
	a.DB.Raw("SELECT count(*) FROM quick_add_links WHERE token_hash = ? OR pin_hash = ?", tok, "1234").Scan(&n)
	if n != 0 {
		t.Errorf("plain values found: %d", n)
	}
}

func TestEnable_Twice(t *testing.T) {
	a := testutil.NewApp(t)
	u, ck := a.NewUser(t, "a@x.test")
	if r := enable(t, a, ck, "1234"); r.Status != 200 {
		t.Fatalf("first: %d", r.Status)
	}
	first := linkOf(t, a, u.ID)
	for i := 0; i < 2; i++ {
		r := enable(t, a, ck, "1234")
		if r.Status != 409 || r.ErrCode() != "QUICK_ADD_ENABLED" {
			t.Fatalf("again: %d %s", r.Status, r.Raw)
		}
	}
	if linkOf(t, a, u.ID).TokenHash != first.TokenHash {
		t.Error("token changed")
	}
}

func TestEnable_AfterDisable(t *testing.T) {
	a := testutil.NewApp(t)
	u, ck := a.NewUser(t, "a@x.test")
	enable(t, a, ck, "1234")
	first := linkOf(t, a, u.ID)
	if r := a.Do(t, "POST", "/api/v1/quick-add/disable", nil, ck); r.Status != 204 {
		t.Fatalf("disable: %d", r.Status)
	}
	if r := enable(t, a, ck, "1234"); r.Status != 200 {
		t.Fatalf("re-enable: %d %s", r.Status, r.Raw)
	}
	l := linkOf(t, a, u.ID)
	if l.TokenHash == first.TokenHash || !l.Enabled {
		t.Errorf("link %+v", l)
	}
}

func TestGet_Off(t *testing.T) {
	a := testutil.NewApp(t)
	_, ck := a.NewUser(t, "a@x.test")
	r := a.Do(t, "GET", "/api/v1/quick-add", nil, ck)
	ev, ok := r.Body["lockEvents"].([]any)
	if r.Status != 200 || r.Body["enabled"] != false || r.Body["lockedUntil"] != nil || !ok || len(ev) != 0 {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
}

func TestGet_EnabledWithLockEvents(t *testing.T) {
	now := time.Date(2026, 10, 20, 5, 0, 0, 0, time.UTC)
	testutil.SetNow(t, now)
	a := testutil.NewApp(t)
	u, ck := a.NewUser(t, "a@x.test")
	enable(t, a, ck, "1234")
	l := linkOf(t, a, u.ID)
	for i := 0; i < 12; i++ {
		a.DB.Create(&models.QuickAddLockEvent{LinkID: l.ID, LockedAt: now.Add(-time.Duration(i) * time.Hour)})
	}
	a.DB.Model(&l).Update("locked_until", now.Add(10*time.Minute))
	r := a.Do(t, "GET", "/api/v1/quick-add", nil, ck)
	var out struct {
		Enabled     bool
		LockedUntil *time.Time
		LockEvents  []struct{ LockedAt time.Time }
	}
	r.Decode(t, &out)
	if !out.Enabled || out.LockedUntil == nil || len(out.LockEvents) != 10 {
		t.Fatalf("got %s", r.Raw)
	}
	if !out.LockEvents[0].LockedAt.Equal(now) || !out.LockEvents[9].LockedAt.Equal(now.Add(-9*time.Hour)) {
		t.Errorf("order wrong: %s", r.Raw)
	}
}

func TestGet_ExpiredLockHidden(t *testing.T) {
	now := time.Date(2026, 10, 20, 5, 0, 0, 0, time.UTC)
	testutil.SetNow(t, now)
	a := testutil.NewApp(t)
	u, ck := a.NewUser(t, "a@x.test")
	enable(t, a, ck, "1234")
	a.DB.Model(&models.QuickAddLink{}).Where("user_id = ?", u.ID).Update("locked_until", now.Add(-time.Minute))
	r := a.Do(t, "GET", "/api/v1/quick-add", nil, ck)
	if r.Status != 200 || r.Body["lockedUntil"] != nil {
		t.Fatalf("got %s", r.Raw)
	}
}

func TestChangePin(t *testing.T) {
	now := time.Date(2026, 10, 20, 5, 0, 0, 0, time.UTC)
	testutil.SetNow(t, now)
	a := testutil.NewApp(t)
	u, ck := a.NewUser(t, "a@x.test")
	enable(t, a, ck, "1234")
	before := linkOf(t, a, u.ID)
	a.DB.Model(&before).Updates(map[string]any{"failed_attempts": 3, "locked_until": now.Add(10 * time.Minute)})
	addSession(t, a, before.ID, now.Add(30*time.Minute))
	r := a.Do(t, "PUT", "/api/v1/quick-add/pin", map[string]string{"pin": "4321", "pinConfirm": "4321"}, ck)
	if r.Status != 204 {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
	l := linkOf(t, a, u.ID)
	if l.TokenHash != before.TokenHash || l.FailedAttempts != 0 || l.LockedUntil != nil {
		t.Errorf("link %+v", l)
	}
	if !secure.CheckPassword(l.PinHash, "4321") || secure.CheckPassword(l.PinHash, "1234") {
		t.Error("pin not changed")
	}
	if n := sessionCount(t, a, l.ID); n != 1 {
		t.Errorf("sessions = %d", n)
	}
}

func TestChangePin_Validation(t *testing.T) {
	a := testutil.NewApp(t)
	u, ck := a.NewUser(t, "a@x.test")
	enable(t, a, ck, "1234")
	r := a.Do(t, "PUT", "/api/v1/quick-add/pin", map[string]string{"pin": "4321", "pinConfirm": "4322"}, ck)
	if r.Status != 400 || r.Field("pinConfirm") != "PINs don't match" {
		t.Errorf("mismatch: %d %s", r.Status, r.Raw)
	}
	r = a.Do(t, "PUT", "/api/v1/quick-add/pin", map[string]string{"pin": "12a4", "pinConfirm": "12a4"}, ck)
	if r.Status != 400 || r.Field("pin") != "PIN must be exactly 4 digits" {
		t.Errorf("invalid: %d %s", r.Status, r.Raw)
	}
	if !secure.CheckPassword(linkOf(t, a, u.ID).PinHash, "1234") {
		t.Error("old pin no longer valid")
	}
}

func TestChangePin_WhenOff(t *testing.T) {
	a := testutil.NewApp(t)
	_, ck := a.NewUser(t, "a@x.test")
	body := map[string]string{"pin": "4321", "pinConfirm": "4321"}
	if r := a.Do(t, "PUT", "/api/v1/quick-add/pin", body, ck); r.Status != 400 || r.ErrCode() != "QUICK_ADD_DISABLED" {
		t.Errorf("never enabled: %d %s", r.Status, r.Raw)
	}
	enable(t, a, ck, "1234")
	a.Do(t, "POST", "/api/v1/quick-add/disable", nil, ck)
	if r := a.Do(t, "PUT", "/api/v1/quick-add/pin", body, ck); r.Status != 400 || r.ErrCode() != "QUICK_ADD_DISABLED" {
		t.Errorf("disabled: %d %s", r.Status, r.Raw)
	}
}

func TestRegenerate(t *testing.T) {
	a := testutil.NewApp(t)
	u, ck := a.NewUser(t, "a@x.test")
	first := tokenOf(t, enable(t, a, ck, "1234"))
	before := linkOf(t, a, u.ID)
	addSession(t, a, before.ID, time.Now().Add(time.Hour))
	r := a.Do(t, "POST", "/api/v1/quick-add/regenerate", nil, ck)
	if r.Status != 200 {
		t.Fatalf("got %d %s", r.Status, r.Raw)
	}
	tok := tokenOf(t, r)
	l := linkOf(t, a, u.ID)
	if tok == first || l.TokenHash == before.TokenHash || l.TokenHash != secure.HashToken(tok) {
		t.Error("token not regenerated")
	}
	if l.PinHash != before.PinHash || !l.Enabled {
		t.Error("pin/enabled changed")
	}
	if n := sessionCount(t, a, l.ID); n != 0 {
		t.Errorf("sessions = %d", n)
	}
}

func TestRegenerate_WhenOff(t *testing.T) {
	a := testutil.NewApp(t)
	_, ck := a.NewUser(t, "a@x.test")
	if r := a.Do(t, "POST", "/api/v1/quick-add/regenerate", nil, ck); r.Status != 400 || r.ErrCode() != "QUICK_ADD_DISABLED" {
		t.Errorf("never enabled: %d %s", r.Status, r.Raw)
	}
	enable(t, a, ck, "1234")
	a.Do(t, "POST", "/api/v1/quick-add/disable", nil, ck)
	if r := a.Do(t, "POST", "/api/v1/quick-add/regenerate", nil, ck); r.Status != 400 || r.ErrCode() != "QUICK_ADD_DISABLED" {
		t.Errorf("disabled: %d %s", r.Status, r.Raw)
	}
}

func TestDisable(t *testing.T) {
	a := testutil.NewApp(t)
	u, ck := a.NewUser(t, "a@x.test")
	enable(t, a, ck, "1234")
	l := linkOf(t, a, u.ID)
	addSession(t, a, l.ID, time.Now().Add(time.Hour))
	if r := a.Do(t, "POST", "/api/v1/quick-add/disable", nil, ck); r.Status != 204 {
		t.Fatalf("got %d", r.Status)
	}
	if linkOf(t, a, u.ID).Enabled || sessionCount(t, a, l.ID) != 0 {
		t.Error("not disabled / sessions remain")
	}
	if r := a.Do(t, "GET", "/api/v1/quick-add", nil, ck); r.Body["enabled"] != false {
		t.Errorf("get: %s", r.Raw)
	}
}

func TestDisable_WhenOff(t *testing.T) {
	a := testutil.NewApp(t)
	_, ck := a.NewUser(t, "a@x.test")
	for i := 0; i < 2; i++ {
		if r := a.Do(t, "POST", "/api/v1/quick-add/disable", nil, ck); r.Status != 204 {
			t.Errorf("disable #%d: %d", i, r.Status)
		}
	}
}

func TestSettings_PerUser(t *testing.T) {
	a := testutil.NewApp(t)
	ua, ca := a.NewUser(t, "a@x.test")
	_, cb := a.NewUser(t, "b@x.test")
	enable(t, a, ca, "1234")
	before := linkOf(t, a, ua.ID)
	if r := a.Do(t, "GET", "/api/v1/quick-add", nil, cb); r.Body["enabled"] != false {
		t.Errorf("B sees enabled: %s", r.Raw)
	}
	if r := a.Do(t, "POST", "/api/v1/quick-add/regenerate", nil, cb); r.Status != 400 || r.ErrCode() != "QUICK_ADD_DISABLED" {
		t.Errorf("B regenerate: %d %s", r.Status, r.Raw)
	}
	if linkOf(t, a, ua.ID).TokenHash != before.TokenHash {
		t.Error("A token changed")
	}
}
