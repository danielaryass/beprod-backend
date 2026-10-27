package isolation_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"beprod/internal/auth"
	"beprod/internal/models"
	"beprod/internal/testutil"
)

var fixedNow = time.Date(2026, 10, 20, 5, 0, 0, 0, time.UTC)

// must memastikan status, kalau tidak t.Fatalf dengan method, path, dan isi response.
func must(t *testing.T, r testutil.Response, wantStatus int, what string) {
	t.Helper()
	if r.Status != wantStatus {
		t.Fatalf("%s: status %d, want %d: %s", what, r.Status, wantStatus, string(r.Raw))
	}
}

// dig mengambil string di Body mengikuti kunci bertingkat.
func dig(t *testing.T, r testutil.Response, keys ...string) string {
	t.Helper()
	var cur any = r.Body
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("dig %v: not an object at %q: %s", keys, k, string(r.Raw))
		}
		cur = m[k]
	}
	s, ok := cur.(string)
	if !ok {
		t.Fatalf("dig %v: not a string: %s", keys, string(r.Raw))
	}
	return s
}

func list(t *testing.T, r testutil.Response, key string) []any {
	t.Helper()
	l, ok := r.Body[key].([]any)
	if !ok {
		t.Fatalf("%q is not a list: %s", key, string(r.Raw))
	}
	return l
}

// hasID melaporkan apakah list berisi objek dengan id tertentu; kembalikan objeknya.
func hasID(l []any, id string) map[string]any {
	for _, e := range l {
		if m, ok := e.(map[string]any); ok && m["id"] == id {
			return m
		}
	}
	return nil
}

type fixture struct {
	Cookie     string
	Prefix     string
	Acc1, Acc2 string
	Cat        string
	Tx         string
	Budget     string
	Rule       string
	Goal       string
	Pocket     string
	Occ        string
	Note       string
	Tag        string
	Habit      string
	Prompt     string
}

const today = "2026-10-20" // tanggal Jakarta dari fixedNow

func buildFixture(t *testing.T, a *testutil.TestApp, cookie, prefix string) fixture {
	t.Helper()
	f := fixture{Cookie: cookie, Prefix: prefix, Note: "secret note " + prefix, Tag: "secret" + strings.ToLower(prefix)}
	post := func(path string, body any, what string) testutil.Response {
		r := a.Do(t, "POST", "/api/v1/"+path, body, cookie)
		must(t, r, 201, what)
		return r
	}
	f.Acc1 = dig(t, post("accounts", map[string]any{"name": prefix + " Cash", "type": "cash", "openingBalance": 100000}, "account 1"), "account", "id")
	f.Acc2 = dig(t, post("accounts", map[string]any{"name": prefix + " Bank", "type": "bank", "openingBalance": 0}, "account 2"), "account", "id")
	f.Cat = dig(t, post("categories", map[string]any{"name": prefix + " Food", "type": "expense", "icon": "utensils", "color": "red"}, "category"), "category", "id")
	f.Tx = dig(t, post("transactions", map[string]any{"type": "expense", "amount": 5000, "accountId": f.Acc1, "categoryId": f.Cat,
		"note": f.Note, "tags": []string{f.Tag}, "date": "2026-10-20", "time": "10:00"}, "transaction"), "transaction", "id")
	f.Budget = dig(t, post("budgets", map[string]any{"categoryId": f.Cat, "monthlyLimit": 100000, "rolloverEnabled": false}, "budget"), "budget", "id")
	f.Rule = dig(t, post("recurring/rules", map[string]any{"type": "expense", "amount": 2000, "accountId": f.Acc1, "categoryId": f.Cat,
		"note": "rule " + prefix, "tags": []string{}, "frequency": "monthly", "startDate": "2026-10-20"}, "rule"), "rule", "id")
	f.Pocket = dig(t, post("accounts", map[string]any{"name": prefix + " Pocket", "type": "bank", "openingBalance": 0}, "pocket"), "account", "id")
	f.Goal = dig(t, post("goals", map[string]any{"name": prefix + " Laptop", "targetAmount": 1000000, "accountId": f.Pocket}, "goal"), "goal", "id")
	r := a.Do(t, "GET", "/api/v1/recurring", nil, cookie)
	must(t, r, 200, "GET /recurring")
	due, _ := r.Body["due"].([]any)
	if len(due) == 0 {
		t.Fatal("no pending occurrence")
	}
	f.Occ, _ = due[0].(map[string]any)["id"].(string)
	f.Habit = dig(t, post("habits", map[string]any{"name": prefix + " Water", "kind": "check", "days": []int{1, 2, 3, 4, 5, 6, 7}}, "habit"), "habit", "id")
	f.Prompt = dig(t, post("note-prompts", map[string]any{"label": prefix + " Mood", "kind": "rating"}, "prompt"), "prompt", "id")
	must(t, a.Do(t, "PUT", "/api/v1/daily/"+today+"/habits/"+f.Habit, map[string]any{"value": 1}, cookie), 200, "log habit")
	must(t, a.Do(t, "PUT", "/api/v1/daily/"+today+"/notes/"+f.Prompt, map[string]any{"rating": 3}, cookie), 200, "answer prompt")
	return f
}

func (f fixture) ids() []string {
	return []string{f.Goal, f.Pocket, f.Prefix + " Laptop", f.Prefix + " Pocket", f.Acc1, f.Acc2, f.Cat, f.Tx, f.Budget, f.Rule, f.Occ,
		f.Prefix + " Cash", f.Prefix + " Bank", f.Prefix + " Food", f.Note, f.Tag,
		f.Habit, f.Prompt, f.Prefix + " Water", f.Prefix + " Mood"}
}

func world(t *testing.T) (a *testutil.TestApp, A, B fixture) {
	t.Helper()
	testutil.SetNow(t, fixedNow)
	a = testutil.NewApp(t)
	_, ca := a.NewUser(t, "a@example.com")
	_, cb := a.NewUser(t, "b@example.com")
	A = buildFixture(t, a, ca, "A")
	B = buildFixture(t, a, cb, "B")
	return
}

// assertUntouched memastikan data A utuh (dibaca sebagai A).
func assertUntouched(t *testing.T, a *testutil.TestApp, A fixture) {
	t.Helper()
	get := func(path string) testutil.Response {
		r := a.Do(t, "GET", "/api/v1/"+path, nil, A.Cookie)
		must(t, r, 200, "A: GET /"+path)
		return r
	}
	if n := dig(t, get("accounts/"+A.Acc1), "account", "name"); n != "A Cash" {
		t.Errorf("A account name changed: %q", n)
	}
	accs := list(t, get("accounts"), "accounts")
	if hasID(accs, A.Acc1) == nil || hasID(accs, A.Acc2) == nil {
		t.Errorf("A accounts missing: %v", accs)
	}
	if c := hasID(list(t, get("categories"), "categories"), A.Cat); c == nil || c["status"] != "active" || c["name"] != "A Food" {
		t.Errorf("A category changed: %v", c)
	}
	tx := get("transactions/" + A.Tx)
	if m, _ := tx.Body["transaction"].(map[string]any); m == nil || m["amount"] != float64(5000) || m["note"] != A.Note {
		t.Errorf("A transaction changed: %s", string(tx.Raw))
	}
	if g := hasID(list(t, get("goals"), "goals"), A.Goal); g == nil || g["name"] != "A Laptop" {
		t.Errorf("A goal changed: %v", g)
	}
	if hasID(list(t, get("budgets?month=2026-10"), "budgets"), A.Budget) == nil {
		t.Errorf("A budget missing")
	}
	rec := get("recurring")
	if r := hasID(list(t, rec, "rules"), A.Rule); r == nil || r["status"] != "active" {
		t.Errorf("A rule changed: %v", r)
	}
	if o := hasID(list(t, rec, "due"), A.Occ); o == nil || o["status"] != "pending" {
		t.Errorf("A occurrence changed: %v", o)
	}
}

func TestIsolation_ForeignIDsReturn404(t *testing.T) {
	a, A, B := world(t)
	type tc struct {
		name   string
		method string
		path   func(A, B fixture) string
		body   func(A, B fixture) any
	}
	acc := func(suffix string) func(A, B fixture) string {
		return func(A, B fixture) string { return "/api/v1/accounts/" + A.Acc1 + suffix }
	}
	cat := func(suffix string) func(A, B fixture) string {
		return func(A, B fixture) string { return "/api/v1/categories/" + A.Cat + suffix }
	}
	rule := func(suffix string) func(A, B fixture) string {
		return func(A, B fixture) string { return "/api/v1/recurring/rules/" + A.Rule + suffix }
	}
	bud := func(suffix string) func(A, B fixture) string {
		return func(A, B fixture) string { return "/api/v1/budgets/" + A.Budget + suffix }
	}
	goal := func(suffix string) func(A, B fixture) string {
		return func(A, B fixture) string { return "/api/v1/goals/" + A.Goal + suffix }
	}
	occ := func(suffix string) func(A, B fixture) string {
		return func(A, B fixture) string { return "/api/v1/recurring/occurrences/" + A.Occ + suffix }
	}
	habit := func(suffix string) func(A, B fixture) string {
		return func(A, B fixture) string { return "/api/v1/habits/" + A.Habit + suffix }
	}
	prompt := func(suffix string) func(A, B fixture) string {
		return func(A, B fixture) string { return "/api/v1/note-prompts/" + A.Prompt + suffix }
	}
	tx := func(A, B fixture) string { return "/api/v1/transactions/" + A.Tx }
	none := func(A, B fixture) any { return nil }
	lit := func(v any) func(A, B fixture) any { return func(A, B fixture) any { return v } }
	cases := []tc{
		{"accounts get", "GET", acc(""), none},
		{"accounts patch", "PATCH", acc(""), lit(map[string]any{"name": "hacked"})},
		{"accounts delete-preview", "GET", acc("/delete-preview"), none},
		{"accounts delete", "DELETE", acc(""), none},
		{"accounts unarchive", "POST", acc("/unarchive"), none},
		{"accounts adjust", "POST", acc("/adjust"), lit(map[string]any{"balance": 1})},
		{"categories patch", "PATCH", cat(""), lit(map[string]any{"name": "hacked"})},
		{"categories delete-preview", "GET", cat("/delete-preview"), none},
		{"categories delete", "DELETE", cat(""), none},
		{"categories unarchive", "POST", cat("/unarchive"), none},
		{"transactions get", "GET", tx, none},
		{"transactions put", "PUT", tx, func(A, B fixture) any {
			return map[string]any{"type": "expense", "amount": 1, "accountId": B.Acc1, "categoryId": B.Cat, "note": "x", "tags": []string{}, "date": "2026-10-20", "time": "10:00"}
		}},
		{"transactions delete", "DELETE", tx, none},
		{"budgets patch", "PATCH", bud(""), lit(map[string]any{"monthlyLimit": 1})},
		{"budgets activate", "POST", bud("/activate"), none},
		{"budgets delete", "DELETE", bud(""), none},
		{"recurring rule put", "PUT", rule(""), func(A, B fixture) any {
			return map[string]any{"amount": 1, "accountId": B.Acc1, "categoryId": B.Cat, "note": "x", "tags": []string{}}
		}},
		{"recurring rule pause", "POST", rule("/pause"), none},
		{"recurring rule resume", "POST", rule("/resume"), none},
		{"recurring rule delete", "DELETE", rule(""), none},
		{"goals patch", "PATCH", goal(""), lit(map[string]any{"name": "hacked"})},
		{"goals complete", "POST", goal("/complete"), none},
		{"goals delete", "DELETE", goal(""), none},
		{"occurrence confirm", "POST", occ("/confirm"), lit(map[string]any{})},
		{"occurrence skip", "POST", occ("/skip"), none},
		{"habits patch", "PATCH", habit(""), lit(map[string]any{"name": "hacked"})},
		{"habits delete", "DELETE", habit(""), none},
		{"note-prompts patch", "PATCH", prompt(""), lit(map[string]any{"label": "hacked"})},
		{"note-prompts delete", "DELETE", prompt(""), none},
		{"daily log habit", "PUT", func(A, B fixture) string { return "/api/v1/daily/" + today + "/habits/" + A.Habit }, lit(map[string]any{"value": 1})},
		{"daily answer prompt", "PUT", func(A, B fixture) string { return "/api/v1/daily/" + today + "/notes/" + A.Prompt }, lit(map[string]any{"rating": 5})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := c.path(A, B)
			r := a.Do(t, c.method, path, c.body(A, B), B.Cookie)
			if r.Status != 404 || r.ErrCode() != "NOT_FOUND" {
				t.Errorf("LEAK: %s %s -> %d %s (want 404 NOT_FOUND)", c.method, path, r.Status, string(r.Raw))
			}
		})
	}
	assertUntouched(t, a, A)
}

func TestIsolation_OrderEndpointsRejectForeignIDs(t *testing.T) {
	a, A, B := world(t)
	r := a.Do(t, "PUT", "/api/v1/accounts/order", map[string]any{"ids": []string{A.Acc1}}, B.Cookie)
	if r.Status != 400 || r.ErrCode() != "INVALID_ORDER" {
		t.Errorf("LEAK: PUT /accounts/order -> %d %s (want 400 INVALID_ORDER)", r.Status, string(r.Raw))
	}
	r = a.Do(t, "PUT", "/api/v1/categories/order", map[string]any{"type": "expense", "ids": []string{A.Cat}}, B.Cookie)
	if r.Status != 400 || r.ErrCode() != "INVALID_ORDER" {
		t.Errorf("LEAK: PUT /categories/order -> %d %s (want 400 INVALID_ORDER)", r.Status, string(r.Raw))
	}
	r = a.Do(t, "PUT", "/api/v1/habits/order", map[string]any{"ids": []string{B.Habit, A.Habit}}, B.Cookie)
	if r.Status != 400 || r.ErrCode() != "INVALID_ORDER" {
		t.Errorf("LEAK: PUT /habits/order -> %d %s (want 400 INVALID_ORDER)", r.Status, string(r.Raw))
	}
	r = a.Do(t, "PUT", "/api/v1/note-prompts/order", map[string]any{"ids": []string{B.Prompt, A.Prompt}}, B.Cookie)
	if r.Status != 400 || r.ErrCode() != "INVALID_ORDER" {
		t.Errorf("LEAK: PUT /note-prompts/order -> %d %s (want 400 INVALID_ORDER)", r.Status, string(r.Raw))
	}
	assertUntouched(t, a, A)
	r = a.Do(t, "GET", "/api/v1/accounts", nil, A.Cookie)
	must(t, r, 200, "A: GET /accounts")
	if accs := list(t, r, "accounts"); len(accs) == 0 || accs[0].(map[string]any)["id"] != A.Acc1 {
		t.Errorf("A account order changed: %s", string(r.Raw))
	}
}

func TestIsolation_CrossReferencesInCreateBodies(t *testing.T) {
	a, A, B := world(t)
	const noAcc, noCat = "This account is no longer available", "This category is no longer available"
	txBody := func(typ, acc, to, cat string) map[string]any {
		m := map[string]any{"type": typ, "amount": 1000, "accountId": acc, "note": "", "tags": []string{}, "date": "2026-10-20", "time": "10:00"}
		if to != "" {
			m["toAccountId"] = to
		}
		if cat != "" {
			m["categoryId"] = cat
		}
		return m
	}
	ruleBody := func(acc, cat string) map[string]any {
		return map[string]any{"type": "expense", "amount": 1000, "accountId": acc, "categoryId": cat, "note": "", "tags": []string{}, "frequency": "monthly", "startDate": "2026-10-20"}
	}
	ruleUpd := func(acc string) map[string]any {
		return map[string]any{"amount": 1000, "accountId": acc, "categoryId": B.Cat, "note": "", "tags": []string{}}
	}
	cases := []struct {
		name, method, path string
		body               any
		field, msg         string
	}{
		{"tx create foreign account", "POST", "/api/v1/transactions", txBody("expense", A.Acc1, "", B.Cat), "accountId", noAcc},
		{"tx create foreign category", "POST", "/api/v1/transactions", txBody("expense", B.Acc1, "", A.Cat), "categoryId", noCat},
		{"tx create foreign to-account", "POST", "/api/v1/transactions", txBody("transfer", B.Acc1, A.Acc1, ""), "toAccountId", noAcc},
		{"tx update foreign account", "PUT", "/api/v1/transactions/" + B.Tx, txBody("expense", A.Acc1, "", B.Cat), "accountId", noAcc},
		{"budget create foreign category", "POST", "/api/v1/budgets", map[string]any{"categoryId": A.Cat, "monthlyLimit": 50000, "rolloverEnabled": false}, "categoryId", "Choose an active expense category"},
		{"goal create foreign account", "POST", "/api/v1/goals", map[string]any{"name": "x", "targetAmount": 1000, "accountId": A.Pocket}, "accountId", "Choose one of your accounts"},
		{"goal update foreign account", "PATCH", "/api/v1/goals/" + B.Goal, map[string]any{"accountId": A.Pocket}, "accountId", "Choose one of your accounts"},
		{"rule create foreign account", "POST", "/api/v1/recurring/rules", ruleBody(A.Acc1, B.Cat), "accountId", noAcc},
		{"rule create foreign category", "POST", "/api/v1/recurring/rules", ruleBody(B.Acc1, A.Cat), "categoryId", noCat},
		{"rule update foreign account", "PUT", "/api/v1/recurring/rules/" + B.Rule, ruleUpd(A.Acc1), "accountId", noAcc},
		{"occurrence confirm foreign account", "POST", "/api/v1/recurring/occurrences/" + B.Occ + "/confirm", map[string]any{"accountId": A.Acc1}, "accountId", noAcc},
		{"occurrence confirm foreign category", "POST", "/api/v1/recurring/occurrences/" + B.Occ + "/confirm", map[string]any{"categoryId": A.Cat}, "categoryId", noCat},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := a.Do(t, c.method, c.path, c.body, B.Cookie)
			if r.Status != 400 || r.ErrCode() != "VALIDATION_ERROR" || r.Field(c.field) != c.msg {
				t.Errorf("LEAK: %s %s -> %d %s (want 400 VALIDATION_ERROR %s=%q)", c.method, c.path, r.Status, string(r.Raw), c.field, c.msg)
			}
		})
	}
	r := a.Do(t, "GET", "/api/v1/transactions?month=2026-10", nil, B.Cookie)
	must(t, r, 200, "B: GET /transactions")
	n := 0
	for _, d := range list(t, r, "days") {
		txs, _ := d.(map[string]any)["transactions"].([]any)
		n += len(txs)
	}
	if n != 1 {
		t.Errorf("B has %d transactions, want 1: %s", n, string(r.Raw))
	}
	r = a.Do(t, "GET", "/api/v1/recurring", nil, B.Cookie)
	must(t, r, 200, "B: GET /recurring")
	if o := hasID(list(t, r, "due"), B.Occ); o == nil || o["status"] != "pending" {
		t.Errorf("B occurrence not pending: %v", o)
	}
	assertUntouched(t, a, A)
}

func TestIsolation_ListsDoNotLeak(t *testing.T) {
	a, A, B := world(t)
	r := a.Do(t, "POST", "/api/v1/quick-add/enable", map[string]any{"pin": "1234", "pinConfirm": "1234"}, A.Cookie)
	must(t, r, 200, "A: enable quick-add")

	get := func(cookie, path string) testutil.Response {
		r := a.Do(t, "GET", path, nil, cookie)
		must(t, r, 200, "GET "+path)
		for _, s := range A.ids() {
			if strings.Contains(string(r.Raw), s) {
				t.Errorf("LEAK: GET %s contains %q: %s", path, s, string(r.Raw))
			}
		}
		return r
	}
	r = get(B.Cookie, "/api/v1/accounts")
	if n := len(list(t, r, "accounts")); n != 3 {
		t.Errorf("B accounts = %d, want 3", n)
	}
	if n := len(list(t, r, "archived")); n != 0 {
		t.Errorf("B archived = %d, want 0", n)
	}
	if r.Body["totalBalance"] != float64(95000) {
		t.Errorf("B totalBalance = %v, want 95000 (100000 opening - 5000 expense)", r.Body["totalBalance"])
	}
	r = get(B.Cookie, "/api/v1/goals")
	if n := len(list(t, r, "goals")); n != 1 {
		t.Errorf("B goals = %d, want 1", n)
	}
	get(B.Cookie, "/api/v1/categories")
	get(B.Cookie, "/api/v1/transactions?month=2026-10")
	for _, q := range []string{"accountId=" + A.Acc1, "categoryId=" + A.Cat, "tag=" + A.Tag, "q=secret%20note%20A"} {
		r = get(B.Cookie, "/api/v1/transactions?month=2026-10&"+q)
		if n := len(list(t, r, "days")); n != 0 {
			t.Errorf("LEAK: transactions filter %s returned %d days: %s", q, n, string(r.Raw))
		}
	}
	r = get(B.Cookie, "/api/v1/tags?q=")
	for _, tg := range list(t, r, "tags") {
		if fmt.Sprint(tg) == A.Tag || strings.Contains(fmt.Sprint(tg), A.Tag) {
			t.Errorf("LEAK: tags contains %v", tg)
		}
	}
	r = get(B.Cookie, "/api/v1/budgets?month=2026-10")
	if n := len(list(t, r, "budgets")); n != 1 {
		t.Errorf("B budgets = %d, want 1", n)
	}
	if n := len(list(t, r, "inactive")); n != 0 {
		t.Errorf("B inactive budgets = %d, want 0", n)
	}
	r = get(B.Cookie, "/api/v1/recurring")
	if len(list(t, r, "rules")) != 1 || len(list(t, r, "due")) != 1 {
		t.Errorf("B recurring wrong: %s", string(r.Raw))
	}
	r = get(B.Cookie, "/api/v1/dashboard?month=2026-10")
	if bal, _ := r.Body["balance"].(map[string]any); bal == nil || bal["total"] != float64(95000) {
		t.Errorf("B dashboard balance = %v, want total 95000", r.Body["balance"])
	}

	r = get(B.Cookie, "/api/v1/habits")
	if hs := list(t, r, "habits"); len(hs) != 1 || hasID(hs, B.Habit) == nil {
		t.Errorf("B habits wrong: %s", string(r.Raw))
	}
	r = get(B.Cookie, "/api/v1/note-prompts")
	if ps := list(t, r, "prompts"); len(ps) != 1 || hasID(ps, B.Prompt) == nil {
		t.Errorf("B prompts wrong: %s", string(r.Raw))
	}
	r = get(B.Cookie, "/api/v1/daily?date="+today)
	if hs, ps := list(t, r, "habits"), list(t, r, "prompts"); len(hs) != 1 || hasID(hs, B.Habit) == nil || len(ps) != 1 || hasID(ps, B.Prompt) == nil {
		t.Errorf("B daily wrong: %s", string(r.Raw))
	}

	_, cc := a.NewUser(t, "c@example.com")
	r = get(cc, "/api/v1/accounts")
	if n := len(list(t, r, "accounts")); n != 0 {
		t.Errorf("C accounts = %d, want 0", n)
	}
	r = get(cc, "/api/v1/transactions?month=2026-10")
	if n := len(list(t, r, "days")); n != 0 {
		t.Errorf("C days = %d, want 0", n)
	}
	r = get(cc, "/api/v1/dashboard")
	if r.Body["hasAccounts"] != false {
		t.Errorf("C hasAccounts = %v, want false", r.Body["hasAccounts"])
	}
	r = get(B.Cookie, "/api/v1/quick-add")
	if r.Body["enabled"] != false {
		t.Errorf("LEAK: B quick-add enabled = %v after A enabled", r.Body["enabled"])
	}
}

func TestIsolation_QuickAddCannotUseForeignRefs(t *testing.T) {
	a, A, B := world(t)
	r := a.Do(t, "POST", "/api/v1/quick-add/enable", map[string]any{"pin": "1234", "pinConfirm": "1234"}, B.Cookie)
	must(t, r, 200, "B: enable quick-add")
	token := strings.TrimPrefix(dig(t, r, "url"), "http://app.test/q/")
	r = a.Do(t, "POST", "/api/v1/q/"+token+"/pin", map[string]any{"pin": "1234"}, "")
	must(t, r, 200, "unlock quick-add")
	qa := map[string]string{"Cookie": "beprod_qa=" + r.Cookie("beprod_qa")}

	r = a.DoH(t, "POST", "/api/v1/q/"+token+"/expenses", map[string]any{"amount": 1000, "categoryId": B.Cat, "accountId": A.Acc1, "note": ""}, qa)
	if r.Status != 400 || r.Field("accountId") != "This account is no longer available" {
		t.Errorf("LEAK: quick-add foreign account -> %d %s", r.Status, string(r.Raw))
	}
	r = a.DoH(t, "POST", "/api/v1/q/"+token+"/expenses", map[string]any{"amount": 1000, "categoryId": A.Cat, "accountId": B.Acc1, "note": ""}, qa)
	if r.Status != 400 || r.Field("categoryId") != "This category is no longer available" {
		t.Errorf("LEAK: quick-add foreign category -> %d %s", r.Status, string(r.Raw))
	}
	r = a.DoH(t, "GET", "/api/v1/q/"+token+"/form", nil, qa)
	must(t, r, 200, "GET quick-add form")
	for _, s := range A.ids() {
		if strings.Contains(string(r.Raw), s) {
			t.Errorf("LEAK: quick-add form contains %q", s)
		}
	}
	var n int64
	if err := a.DB.Raw("SELECT count(*) FROM transactions WHERE source = 'quick_add'").Scan(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("quick_add transactions = %d, want 0", n)
	}
}

var forbidden = map[string]bool{"balance": true, "amount": true, "note": true, "tags": true, "openingBalance": true}

func walk(t *testing.T, v any, path string) {
	t.Helper()
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if forbidden[k] {
				t.Errorf("admin response leaks key %q at %s", k, path)
			}
			walk(t, val, path+"."+k)
		}
	case []any:
		for i, e := range x {
			walk(t, e, fmt.Sprintf("%s[%d]", path, i))
		}
	}
}

func TestIsolation_AdminListLeaksNoFinancialData(t *testing.T) {
	a, A, _ := world(t)
	admin := a.CreateUserWith(t, auth.NewUser{Name: "Root", Email: "root@example.com", Role: models.RoleSuperAdmin, Status: models.UserActive})
	ck := a.LoginAs(t, admin.ID)
	r := a.Do(t, "GET", "/api/v1/admin/users", nil, ck)
	must(t, r, 200, "admin: GET /admin/users")
	walk(t, r.Body, "$")
	for _, s := range []string{A.Note, A.Tag, "A Cash", "A Food", A.Prefix + " Water", A.Prefix + " Mood"} {
		if strings.Contains(string(r.Raw), s) {
			t.Errorf("admin response contains %q", s)
		}
	}
	users := list(t, r, "users")
	if len(users) < 3 {
		t.Errorf("admin users = %d, want >= 3", len(users))
	}
	counted := false
	for _, u := range users {
		if c, _ := u.(map[string]any)["transactionCount"].(float64); c >= 1 {
			counted = true
		}
	}
	if !counted {
		t.Errorf("no user has transactionCount >= 1: %s", string(r.Raw))
	}
	r = a.Do(t, "GET", "/api/v1/admin/users", nil, A.Cookie)
	if r.Status != 404 || r.ErrCode() != "NOT_FOUND" {
		t.Errorf("regular user admin/users -> %d %s (want 404 NOT_FOUND)", r.Status, string(r.Raw))
	}
	r = a.Do(t, "GET", "/api/v1/admin/users", nil, "")
	if r.Status != 401 {
		t.Errorf("anonymous admin/users -> %d (want 401)", r.Status)
	}
}
