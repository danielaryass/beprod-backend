package quickadd_test

import (
	"strings"
	"testing"
	"time"

	"beprod/internal/auth"
	"beprod/internal/models"
	"beprod/internal/testutil"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"gorm.io/gorm"
)

var t0 = time.Date(2026, 10, 20, 5, 0, 0, 0, time.UTC) // 12:00 WIB

const testPIN = "1234"

func enableQuickAdd(t *testing.T, a *testutil.TestApp, userCookie, pin string) string {
	t.Helper()
	r := enable(t, a, userCookie, pin)
	if r.Status != 200 {
		t.Fatalf("enable: %d %s", r.Status, r.Raw)
	}
	return tokenOf(t, r)
}

func unlock(t *testing.T, a *testutil.TestApp, token, pin string) string {
	t.Helper()
	r := qaPost(t, a, token, "/pin", map[string]string{"pin": pin}, nil)
	if r.Status != 200 {
		t.Fatalf("unlock: %d %s", r.Status, r.Raw)
	}
	return "beprod_qa=" + r.Cookie("beprod_qa")
}

func qaGet(t *testing.T, a *testutil.TestApp, token, path, cookie string) testutil.Response {
	t.Helper()
	h := map[string]string{}
	if cookie != "" {
		h["Cookie"] = cookie
	}
	return a.DoH(t, "GET", "/api/v1/q/"+token+path, nil, h)
}

func qaPost(t *testing.T, a *testutil.TestApp, token, path string, body any, headers map[string]string) testutil.Response {
	t.Helper()
	return a.DoH(t, "POST", "/api/v1/q/"+token+path, body, headers)
}

func seedAccount(t *testing.T, a *testutil.TestApp, userID uuid.UUID, name string, sort int) models.Account {
	t.Helper()
	acc := models.Account{UserID: userID, Name: name, Type: "cash", OpeningDate: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), SortOrder: sort}
	if err := a.DB.Create(&acc).Error; err != nil {
		t.Fatal(err)
	}
	return acc
}

func catID(t *testing.T, a *testutil.TestApp, userID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	var c models.Category
	if err := a.DB.Where("user_id = ? AND name = ?", userID, name).First(&c).Error; err != nil {
		t.Fatal(err)
	}
	return c.ID
}

func attemptsLeft(t *testing.T, r testutil.Response) float64 {
	t.Helper()
	return r.Body["error"].(map[string]any)["attemptsLeft"].(float64)
}

func newReadyUser(t *testing.T, a *testutil.TestApp, email string) (models.User, string, string, models.Account) {
	t.Helper()
	u, uc := a.NewUser(t, email)
	acc := seedAccount(t, a, u.ID, "Cash", 1)
	return u, uc, enableQuickAdd(t, a, uc, testPIN), acc
}

func wrongPins(t *testing.T, a *testutil.TestApp, token string, n int) testutil.Response {
	var r testutil.Response
	for i := 0; i < n; i++ {
		r = qaPost(t, a, token, "/pin", map[string]string{"pin": "0000"}, nil)
	}
	return r
}

func saveBody(acc models.Account, cat uuid.UUID) map[string]any {
	return map[string]any{"amount": 25000, "categoryId": cat.String(), "accountId": acc.ID.String(), "note": "kopi"}
}

func seedTxs(t *testing.T, a *testutil.TestApp, u models.User, acc models.Account, n int, source string, created time.Time) {
	t.Helper()
	cid := catID(t, a, u.ID, "Food & Drinks")
	for i := 0; i < n; i++ {
		tx := models.Transaction{UserID: u.ID, Type: models.TypeExpense, Amount: 1000, OccurredAt: t0, AccountID: acc.ID,
			CategoryID: &cid, Tags: pq.StringArray{}, Source: source, CreatedAt: created}
		if err := a.DB.Create(&tx).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func txCount(a *testutil.TestApp, userID uuid.UUID) int64 {
	var n int64
	a.DB.Model(&models.Transaction{}).Where("user_id = ?", userID).Count(&n)
	return n
}

func TestPublic_UnknownToken(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	if r := qaGet(t, a, "does-not-exist", "", ""); r.Status != 404 || r.ErrCode() != "LINK_INACTIVE" {
		t.Fatalf("get: %d %s", r.Status, r.Raw)
	}
	if r := qaPost(t, a, "does-not-exist", "/pin", map[string]string{"pin": "1234"}, nil); r.Status != 404 || r.ErrCode() != "LINK_INACTIVE" {
		t.Fatalf("pin: %d %s", r.Status, r.Raw)
	}
	if r := qaGet(t, a, "does-not-exist", "/form", ""); r.Status != 404 {
		t.Fatalf("form: %d", r.Status)
	}
	if r := qaPost(t, a, "does-not-exist", "/expenses", map[string]any{"amount": 1}, nil); r.Status != 404 {
		t.Fatalf("expenses: %d", r.Status)
	}
}

func TestPublic_ValidLinkReady(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	u := a.CreateUserWith(t, auth.NewUser{Name: "Zainal Secret", Email: "zainal.secret@example.com", Status: models.UserActive})
	token := enableQuickAdd(t, a, a.LoginAs(t, u.ID), testPIN)
	r := qaGet(t, a, token, "", "")
	if r.Status != 200 || r.Body["status"] != "ready" || r.Body["sessionActive"] != false {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	for _, s := range []string{"Zainal", "Secret", "zainal.secret", "@example.com"} {
		if strings.Contains(string(r.Raw), s) {
			t.Fatalf("leaks %q: %s", s, r.Raw)
		}
	}
}

func TestPin_WrongAttemptsCountDown(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	u, _, token, _ := newReadyUser(t, a, "a@example.com")
	for i, want := range []float64{4, 3, 2, 1} {
		r := qaPost(t, a, token, "/pin", map[string]string{"pin": "0000"}, nil)
		if r.Status != 401 || r.ErrCode() != "WRONG_PIN" || attemptsLeft(t, r) != want {
			t.Fatalf("#%d: %d %s", i, r.Status, r.Raw)
		}
		if i == 0 && !strings.Contains(string(r.Raw), "Wrong PIN (4 attempts left)") {
			t.Fatalf("message: %s", r.Raw)
		}
	}
	if n := linkOf(t, a, u.ID).FailedAttempts; n != 4 {
		t.Fatalf("failed attempts %d", n)
	}
}

func TestPin_FifthWrongLocks(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	u, _, token, _ := newReadyUser(t, a, "a@example.com")
	r := wrongPins(t, a, token, 5)
	if r.Status != 423 || r.ErrCode() != "LINK_LOCKED" || r.Body["error"].(map[string]any)["retryAfterMinutes"].(float64) != 15 ||
		!strings.Contains(string(r.Raw), "Too many attempts. Try again in 15 minutes.") {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	l := linkOf(t, a, u.ID)
	var evs []models.QuickAddLockEvent
	a.DB.Where("link_id = ?", l.ID).Find(&evs)
	if len(evs) != 1 || !evs[0].LockedAt.Equal(t0) {
		t.Fatalf("events %+v", evs)
	}
	if l.LockedUntil == nil || !l.LockedUntil.Equal(t0.Add(15*time.Minute)) || l.FailedAttempts != 0 {
		t.Fatalf("link %+v", l)
	}
}

func TestPin_LockedRejectsCorrectPin(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	u, _, token, _ := newReadyUser(t, a, "a@example.com")
	wrongPins(t, a, token, 5)
	r := qaPost(t, a, token, "/pin", map[string]string{"pin": testPIN}, nil)
	if r.Status != 423 || r.Cookie("beprod_qa") != "" {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	if r := qaGet(t, a, token, "", ""); r.Status != 423 {
		t.Fatalf("get %d", r.Status)
	}
	var n int64
	a.DB.Model(&models.QuickAddLockEvent{}).Where("link_id = ?", linkOf(t, a, u.ID).ID).Count(&n)
	if n != 1 {
		t.Fatalf("events %d", n)
	}
}

func TestPin_UnlocksAfter15Minutes(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	_, _, token, _ := newReadyUser(t, a, "a@example.com")
	wrongPins(t, a, token, 5)
	testutil.SetNow(t, t0.Add(16*time.Minute))
	r := qaPost(t, a, token, "/pin", map[string]string{"pin": testPIN}, nil)
	if r.Status != 200 || r.Body["status"] != "ok" || r.Cookie("beprod_qa") == "" {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
}

func TestPin_CorrectResetsCounter(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	u, _, token, _ := newReadyUser(t, a, "a@example.com")
	wrongPins(t, a, token, 3)
	unlock(t, a, token, testPIN)
	if n := linkOf(t, a, u.ID).FailedAttempts; n != 0 {
		t.Fatalf("failed attempts %d", n)
	}
	if r := wrongPins(t, a, token, 1); attemptsLeft(t, r) != 4 {
		t.Fatalf("%s", r.Raw)
	}
}

func TestPin_DummyHashWorksWithoutLink(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	for i := 0; i < 2; i++ {
		if r := qaPost(t, a, "nope", "/pin", map[string]string{"pin": "1234"}, nil); r.Status != 404 {
			t.Fatalf("%d", r.Status)
		}
	}
}

func TestForm_RequiresQASession(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	_, _, token, _ := newReadyUser(t, a, "a@example.com")
	for _, ck := range []string{"", "beprod_qa=garbage"} {
		if r := qaGet(t, a, token, "/form", ck); r.Status != 401 || r.ErrCode() != "QA_SESSION_REQUIRED" {
			t.Fatalf("%q: %d %s", ck, r.Status, r.Raw)
		}
	}
}

func TestForm_AccountsAndCategoriesOnly(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	u, _, token, _ := newReadyUser(t, a, "a@example.com")
	seedAccount(t, a, u.ID, "Bank", 2)
	arch := seedAccount(t, a, u.ID, "Old", 3)
	a.DB.Model(&arch).Update("status", models.StatusArchived)
	a.DB.Model(&models.Category{}).Where("id = ?", catID(t, a, u.ID, "Transport")).Update("status", models.StatusArchived)
	ck := unlock(t, a, token, testPIN)
	r := qaGet(t, a, token, "/form", ck)
	if r.Status != 200 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	accs := r.Body["accounts"].([]any)
	if len(accs) != 2 || accs[0].(map[string]any)["name"] != "Cash" || accs[1].(map[string]any)["name"] != "Bank" {
		t.Fatalf("accounts %v", accs)
	}
	for _, x := range accs {
		if len(x.(map[string]any)) != 3 {
			t.Fatalf("account keys %v", x)
		}
	}
	cats := r.Body["categories"].([]any)
	if len(cats) != 8 {
		t.Fatalf("categories %d", len(cats))
	}
	for _, x := range cats {
		m := x.(map[string]any)
		if len(m) != 4 || m["name"] == "Salary" || m["name"] == "Transport" {
			t.Fatalf("category %v", m)
		}
	}
	if strings.Contains(strings.ToLower(string(r.Raw)), "balance") || r.Body["setupRequired"] != false {
		t.Fatalf("%s", r.Raw)
	}
}

func TestForm_LastUsedCategoryFirst(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	u, _, token, acc := newReadyUser(t, a, "a@example.com")
	for name, at := range map[string]time.Time{"Health": t0.Add(-48 * time.Hour), "Education": t0.Add(-24 * time.Hour)} {
		cid := catID(t, a, u.ID, name)
		tx := models.Transaction{UserID: u.ID, Type: models.TypeExpense, Amount: 1000, OccurredAt: at, AccountID: acc.ID,
			CategoryID: &cid, Tags: pq.StringArray{}, Source: models.SourceApp}
		if err := a.DB.Create(&tx).Error; err != nil {
			t.Fatal(err)
		}
	}
	r := qaGet(t, a, token, "/form", unlock(t, a, token, testPIN))
	cats := r.Body["categories"].([]any)
	name := func(i int) any { return cats[i].(map[string]any)["name"] }
	if len(cats) != 9 || name(0) != "Education" || name(1) != "Health" || name(2) != "Food & Drinks" {
		t.Fatalf("%v", cats)
	}
}

func TestForm_DefaultAccountIsLastUsed(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	u, _, token, accA := newReadyUser(t, a, "a@example.com")
	accB := seedAccount(t, a, u.ID, "Bank", 2)
	ck := unlock(t, a, token, testPIN)
	def := func() any { return qaGet(t, a, token, "/form", ck).Body["defaultAccountId"] }
	if def() != accA.ID.String() {
		t.Fatal("expected A first")
	}
	if r := qaPost(t, a, token, "/expenses", saveBody(accB, catID(t, a, u.ID, "Food & Drinks")), map[string]string{"Cookie": ck}); r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	if def() != accB.ID.String() {
		t.Fatal("expected B last used")
	}
	a.DB.Model(&accB).Update("status", "archived")
	if def() != accA.ID.String() {
		t.Fatal("expected fallback to A")
	}
}

func TestForm_SetupRequiredWhenNoAccounts(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	_, uc := a.NewUser(t, "a@example.com")
	token := enableQuickAdd(t, a, uc, testPIN)
	r := qaGet(t, a, token, "/form", unlock(t, a, token, testPIN))
	if r.Body["setupRequired"] != true || len(r.Body["accounts"].([]any)) != 0 || r.Body["defaultAccountId"] != nil {
		t.Fatalf("%s", r.Raw)
	}
}

func TestSave_Success(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	u, _, token, acc := newReadyUser(t, a, "a@example.com")
	ck := unlock(t, a, token, testPIN)
	r := qaPost(t, a, token, "/expenses", saveBody(acc, catID(t, a, u.ID, "Food & Drinks")), map[string]string{"Cookie": ck})
	if r.Status != 201 || r.Body["amount"] != float64(25000) || r.Body["categoryName"] != "Food & Drinks" {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	var txs []models.Transaction
	a.DB.Where("user_id = ?", u.ID).Find(&txs)
	if len(txs) != 1 {
		t.Fatalf("txs %d", len(txs))
	}
	x := txs[0]
	if x.Source != "quick_add" || x.Type != "expense" || x.Amount != 25000 || x.Note != "kopi" || !x.OccurredAt.Equal(t0) || len(x.Tags) != 0 {
		t.Fatalf("%+v", x)
	}
	if l := linkOf(t, a, u.ID); l.LastAccountID == nil || *l.LastAccountID != acc.ID {
		t.Fatalf("last account %v", l.LastAccountID)
	}
}

func TestSave_SessionExpires30MinutesAfterPIN(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	u, _, token, acc := newReadyUser(t, a, "a@example.com")
	ck := unlock(t, a, token, testPIN)
	testutil.SetNow(t, t0.Add(31*time.Minute))
	if r := qaGet(t, a, token, "/form", ck); r.Status != 401 || r.ErrCode() != "QA_SESSION_REQUIRED" {
		t.Fatalf("form %d", r.Status)
	}
	if r := qaPost(t, a, token, "/expenses", saveBody(acc, catID(t, a, u.ID, "Food & Drinks")), map[string]string{"Cookie": ck}); r.Status != 401 {
		t.Fatalf("save %d", r.Status)
	}
	if txCount(a, u.ID) != 0 {
		t.Fatal("tx created")
	}
}

func TestSave_ExtendsSession(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	u, _, token, acc := newReadyUser(t, a, "a@example.com")
	ck := unlock(t, a, token, testPIN)
	testutil.SetNow(t, t0.Add(20*time.Minute))
	if r := qaPost(t, a, token, "/expenses", saveBody(acc, catID(t, a, u.ID, "Food & Drinks")), map[string]string{"Cookie": ck}); r.Status != 201 {
		t.Fatalf("save %d", r.Status)
	}
	testutil.SetNow(t, t0.Add(31*time.Minute))
	if r := qaGet(t, a, token, "/form", ck); r.Status != 200 {
		t.Fatalf("form %d", r.Status)
	}
	testutil.SetNow(t, t0.Add(51*time.Minute))
	if r := qaGet(t, a, token, "/form", ck); r.Status != 401 {
		t.Fatalf("form %d", r.Status)
	}
}

func TestSave_IdempotencyKey(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	u, _, token, acc := newReadyUser(t, a, "a@example.com")
	ck := unlock(t, a, token, testPIN)
	body := saveBody(acc, catID(t, a, u.ID, "Food & Drinks"))
	h := map[string]string{"Cookie": ck, "Idempotency-Key": "k-1"}
	r1 := qaPost(t, a, token, "/expenses", body, h)
	r2 := qaPost(t, a, token, "/expenses", body, h)
	if r1.Status != 201 || r2.Status != 200 || r2.Body["amount"] != r1.Body["amount"] || r2.Body["categoryName"] != r1.Body["categoryName"] {
		t.Fatalf("%d %s / %d %s", r1.Status, r1.Raw, r2.Status, r2.Raw)
	}
	var n int64
	a.DB.Model(&models.Transaction{}).Where("user_id = ? AND idempotency_key = ?", u.ID, "k-1").Count(&n)
	if n != 1 || txCount(a, u.ID) != 1 {
		t.Fatalf("count %d", n)
	}
	h["Idempotency-Key"] = strings.Repeat("k", 65)
	if r := qaPost(t, a, token, "/expenses", body, h); r.Status != 400 || r.ErrCode() != "INVALID_IDEMPOTENCY_KEY" {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
}

func TestSave_RateLimit31st(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	u, _, token, acc := newReadyUser(t, a, "a@example.com")
	ck := unlock(t, a, token, testPIN)
	seedTxs(t, a, u, acc, 29, models.SourceQuickAdd, t0)
	body := saveBody(acc, catID(t, a, u.ID, "Food & Drinks"))
	h := map[string]string{"Cookie": ck}
	if r := qaPost(t, a, token, "/expenses", body, h); r.Status != 201 {
		t.Fatalf("30th %d %s", r.Status, r.Raw)
	}
	r := qaPost(t, a, token, "/expenses", body, h)
	if r.Status != 429 || r.ErrCode() != "TOO_MANY_ENTRIES" || !strings.Contains(string(r.Raw), "Too many entries. Please wait a moment.") {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
	if txCount(a, u.ID) != 30 {
		t.Fatalf("count %d", txCount(a, u.ID))
	}
}

func TestSave_RateLimitWindowExpires(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	u, _, token, acc := newReadyUser(t, a, "a@example.com")
	ck := unlock(t, a, token, testPIN)
	seedTxs(t, a, u, acc, 30, models.SourceQuickAdd, t0.Add(-11*time.Minute))
	if r := qaPost(t, a, token, "/expenses", saveBody(acc, catID(t, a, u.ID, "Food & Drinks")), map[string]string{"Cookie": ck}); r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
}

func TestSave_RateLimitIgnoresOtherSources(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	u, _, token, acc := newReadyUser(t, a, "a@example.com")
	ck := unlock(t, a, token, testPIN)
	seedTxs(t, a, u, acc, 30, models.SourceApp, t0)
	if r := qaPost(t, a, token, "/expenses", saveBody(acc, catID(t, a, u.ID, "Food & Drinks")), map[string]string{"Cookie": ck}); r.Status != 201 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
}

func TestSave_ArchivedCategoryRejected(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	u, _, token, acc := newReadyUser(t, a, "a@example.com")
	ck := unlock(t, a, token, testPIN)
	food := catID(t, a, u.ID, "Food & Drinks")
	a.DB.Model(&models.Category{}).Where("id = ?", food).Update("status", models.StatusArchived)
	r := qaPost(t, a, token, "/expenses", saveBody(acc, food), map[string]string{"Cookie": ck})
	if r.Status != 400 || r.ErrCode() != "VALIDATION_ERROR" || r.Field("categoryId") != "This category is no longer available" || txCount(a, u.ID) != 0 {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
}

func TestSave_ArchivedAccountRejected(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	u, _, token, acc := newReadyUser(t, a, "a@example.com")
	ck := unlock(t, a, token, testPIN)
	a.DB.Model(&acc).Update("status", models.StatusArchived)
	r := qaPost(t, a, token, "/expenses", saveBody(acc, catID(t, a, u.ID, "Food & Drinks")), map[string]string{"Cookie": ck})
	if r.Status != 400 || r.Field("accountId") != "This account is no longer available" {
		t.Fatalf("%d %s", r.Status, r.Raw)
	}
}

func TestSave_InvalidAmountAndIncomeCategory(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	u, _, token, acc := newReadyUser(t, a, "a@example.com")
	h := map[string]string{"Cookie": unlock(t, a, token, testPIN)}
	b := saveBody(acc, catID(t, a, u.ID, "Food & Drinks"))
	b["amount"] = 0
	if r := qaPost(t, a, token, "/expenses", b, h); r.Field("amount") != "Amount must be greater than 0" {
		t.Fatalf("%s", r.Raw)
	}
	r := qaPost(t, a, token, "/expenses", saveBody(acc, catID(t, a, u.ID, "Salary")), h)
	if r.Field("categoryId") != "Category doesn't match the transaction type" {
		t.Fatalf("%s", r.Raw)
	}
}

func TestLink_RegenerateInvalidatesOldTokenAndSession(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	_, uc, old, _ := newReadyUser(t, a, "a@example.com")
	ck := unlock(t, a, old, testPIN)
	if r := qaGet(t, a, old, "/form", ck); r.Status != 200 {
		t.Fatalf("form %d", r.Status)
	}
	r := a.Do(t, "POST", "/api/v1/quick-add/regenerate", nil, uc)
	if r.Status != 200 {
		t.Fatalf("regen %d %s", r.Status, r.Raw)
	}
	fresh := tokenOf(t, r)
	if r := qaGet(t, a, old, "", ""); r.Status != 404 || r.ErrCode() != "LINK_INACTIVE" {
		t.Fatalf("old %d", r.Status)
	}
	if r := qaGet(t, a, fresh, "/form", ck); r.Status != 401 || r.ErrCode() != "QA_SESSION_REQUIRED" {
		t.Fatalf("new+old cookie %d", r.Status)
	}
	unlock(t, a, fresh, testPIN)
}

func TestLink_DisableMakesLinkInactive(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	_, uc, token, _ := newReadyUser(t, a, "a@example.com")
	ck := unlock(t, a, token, testPIN)
	if r := a.Do(t, "POST", "/api/v1/quick-add/disable", nil, uc); r.Status != 204 {
		t.Fatalf("disable %d", r.Status)
	}
	if r := qaGet(t, a, token, "", ""); r.Status != 404 {
		t.Fatalf("get %d", r.Status)
	}
	if r := qaGet(t, a, token, "/form", ck); r.Status != 404 {
		t.Fatalf("form %d", r.Status)
	}
	if r := qaPost(t, a, token, "/pin", map[string]string{"pin": testPIN}, nil); r.Status != 404 {
		t.Fatalf("pin %d", r.Status)
	}
}

func TestLink_OwnerDeactivated(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	u, _, token, acc := newReadyUser(t, a, "a@example.com")
	ck := unlock(t, a, token, testPIN)
	setStatus := func(s string) *gorm.DB {
		return a.DB.Model(&models.User{}).Where("id = ?", u.ID).Update("status", s)
	}
	setStatus(models.UserDeactivated)
	h := map[string]string{"Cookie": ck}
	for name, r := range map[string]testutil.Response{
		"get":      qaGet(t, a, token, "", ""),
		"form":     qaGet(t, a, token, "/form", ck),
		"expenses": qaPost(t, a, token, "/expenses", saveBody(acc, catID(t, a, u.ID, "Food & Drinks")), h),
		"pin":      qaPost(t, a, token, "/pin", map[string]string{"pin": testPIN}, nil),
	} {
		if r.Status != 404 {
			t.Fatalf("%s %d", name, r.Status)
		}
	}
	setStatus(models.UserActive)
	if r := qaGet(t, a, token, "", ""); r.Status != 200 || r.Body["status"] != "ready" {
		t.Fatalf("get %d", r.Status)
	}
	if r := qaGet(t, a, token, "/form", ck); r.Status != 200 {
		t.Fatalf("form %d", r.Status)
	}
}

func TestLink_QACookieNotValidOnOtherLink(t *testing.T) {
	testutil.SetNow(t, t0)
	a := testutil.NewApp(t)
	_, _, tokA, _ := newReadyUser(t, a, "a@example.com")
	ub, _, tokB, accB := newReadyUser(t, a, "b@example.com")
	ck := unlock(t, a, tokA, testPIN)
	if r := qaGet(t, a, tokB, "/form", ck); r.Status != 401 || r.ErrCode() != "QA_SESSION_REQUIRED" {
		t.Fatalf("form %d", r.Status)
	}
	if r := qaPost(t, a, tokB, "/expenses", saveBody(accB, catID(t, a, ub.ID, "Food & Drinks")), map[string]string{"Cookie": ck}); r.Status != 401 {
		t.Fatalf("save %d", r.Status)
	}
	if txCount(a, ub.ID) != 0 {
		t.Fatal("tx created")
	}
}
