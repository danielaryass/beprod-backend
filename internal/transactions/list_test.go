package transactions_test

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/testutil"
	"beprod/internal/transactions"
)

// dbTxn inserts a transaction directly. Pass "" for toAccountID / categoryID when not used.
func dbTxn(t *testing.T, a *testutil.TestApp, userID uuid.UUID, typ string, amount int64, accountID, toAccountID, categoryID string,
	at time.Time, note string, tags ...string) models.Transaction {
	t.Helper()
	tx := models.Transaction{UserID: userID, Type: typ, Amount: amount, OccurredAt: at, AccountID: uuid.MustParse(accountID),
		Note: note, Tags: pq.StringArray(append([]string{}, tags...)), Source: models.SourceApp}
	if toAccountID != "" {
		id := uuid.MustParse(toAccountID)
		tx.ToAccountID = &id
	}
	if categoryID != "" {
		id := uuid.MustParse(categoryID)
		tx.CategoryID = &id
	}
	if err := a.DB.Create(&tx).Error; err != nil {
		t.Fatalf("insert txn: %v", err)
	}
	return tx
}

// wib builds an instant from WIB wall-clock time.
func wib(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, jakarta.Loc)
}

type listResp struct {
	Days []struct {
		Date         string             `json:"date"`
		NetTotal     int64              `json:"netTotal"`
		Transactions []transactions.DTO `json:"transactions"`
	} `json:"days"`
	Page    int  `json:"page"`
	HasMore bool `json:"hasMore"`
}

func getList(t *testing.T, a *testutil.TestApp, cookie, query string) listResp {
	t.Helper()
	path := txURL
	if query != "" {
		path += "?" + query
	}
	r := a.Do(t, "GET", path, nil, cookie)
	mustStatus(t, r, 200)
	var out listResp
	r.Decode(t, &out)
	return out
}

func (l listResp) count() int {
	n := 0
	for _, d := range l.Days {
		n += len(d.Transactions)
	}
	return n
}

func TestList_DefaultMonthIsCurrentWIBMonth(t *testing.T) {
	a, u, cookie := setup(t)
	acc := apiAccount(t, a, cookie, "Cash", 0)
	dbTxn(t, a, u.ID, models.TypeExpense, 1000, acc, "", "", wib(2026, 10, 5, 9, 0), "oct")
	dbTxn(t, a, u.ID, models.TypeExpense, 1000, acc, "", "", wib(2026, 9, 30, 9, 0), "sep")
	l := getList(t, a, cookie, "")
	if l.count() != 1 || l.Days[0].Transactions[0].Note != "oct" || l.Page != 1 || l.HasMore {
		t.Fatalf("unexpected: %+v", l)
	}
}

func TestList_MonthBoundariesInWIB(t *testing.T) {
	a, u, cookie := setup(t)
	acc := apiAccount(t, a, cookie, "Cash", 0)
	for _, at := range []time.Time{wib(2026, 10, 31, 23, 30), wib(2026, 11, 1, 0, 10), wib(2026, 10, 1, 0, 0), wib(2026, 11, 1, 0, 0), wib(2026, 9, 30, 23, 59)} {
		dbTxn(t, a, u.ID, models.TypeExpense, 1000, acc, "", "", at, "")
	}
	oct := getList(t, a, cookie, "month=2026-10")
	if oct.count() != 2 || oct.Days[0].Date != "2026-10-31" || oct.Days[1].Date != "2026-10-01" {
		t.Fatalf("oct: %+v", oct)
	}
	nov := getList(t, a, cookie, "month=2026-11")
	if nov.count() != 2 || len(nov.Days) != 1 || nov.Days[0].Date != "2026-11-01" {
		t.Fatalf("nov: %+v", nov)
	}
	if sep := getList(t, a, cookie, "month=2026-09"); sep.count() != 1 {
		t.Fatalf("sep: %+v", sep)
	}
}

func TestList_AccountFilter_TransfersInAndOutWithNetTotal(t *testing.T) {
	a, u, cookie := setup(t)
	A := apiAccount(t, a, cookie, "A", 0)
	B := apiAccount(t, a, cookie, "B", 0)
	C := apiAccount(t, a, cookie, "C", 0)
	cat := catID(t, a, u.ID, "Food & Drinks")
	dbTxn(t, a, u.ID, models.TypeExpense, 10000, A, "", cat, wib(2026, 10, 20, 8, 0), "")
	dbTxn(t, a, u.ID, models.TypeIncome, 50000, A, "", "", wib(2026, 10, 20, 9, 0), "")
	dbTxn(t, a, u.ID, models.TypeTransfer, 30000, A, B, "", wib(2026, 10, 20, 10, 0), "")
	dbTxn(t, a, u.ID, models.TypeTransfer, 20000, B, A, "", wib(2026, 10, 20, 11, 0), "")
	dbTxn(t, a, u.ID, models.TypeTransfer, 5000, B, C, "", wib(2026, 10, 20, 12, 0), "")

	la := getList(t, a, cookie, "month=2026-10&accountId="+A)
	if la.count() != 4 || len(la.Days) != 1 || la.Days[0].Date != "2026-10-20" || la.Days[0].NetTotal != 30000 {
		t.Fatalf("A: %+v", la)
	}
	for i, want := range []int64{20000, 30000, 50000, 10000} {
		if got := la.Days[0].Transactions[i].Amount; got != want {
			t.Fatalf("A order[%d] = %d want %d", i, got, want)
		}
	}
	lb := getList(t, a, cookie, "month=2026-10&accountId="+B)
	if lb.count() != 3 || lb.Days[0].NetTotal != 5000 {
		t.Fatalf("B: %+v", lb)
	}
	all := getList(t, a, cookie, "month=2026-10")
	if all.count() != 5 || all.Days[0].NetTotal != 40000 {
		t.Fatalf("all: %+v", all)
	}
	tr := getList(t, a, cookie, "month=2026-10&type=transfer")
	if tr.count() != 3 || tr.Days[0].NetTotal != 0 {
		t.Fatalf("transfer: %+v", tr)
	}
	for _, x := range tr.Days[0].Transactions {
		if x.ToAccount == nil || x.ToAccount.Name == "" {
			t.Fatalf("missing toAccount: %+v", x)
		}
	}
}

func TestList_CategoryTypeTagAndQFilters(t *testing.T) {
	a, u, cookie := setup(t)
	acc := apiAccount(t, a, cookie, "Cash", 0)
	food := catID(t, a, u.ID, "Food & Drinks")
	transport := catID(t, a, u.ID, "Transport")
	salary := catID(t, a, u.ID, "Salary")
	at := wib(2026, 10, 10, 12, 0)
	dbTxn(t, a, u.ID, models.TypeExpense, 10000, acc, "", food, at, "Lunch at warung", "food", "work")
	dbTxn(t, a, u.ID, models.TypeExpense, 20000, acc, "", transport, at.Add(time.Minute), "Grab to office 50% off", "work")
	dbTxn(t, a, u.ID, models.TypeIncome, 90000, acc, "", salary, at.Add(2*time.Minute), "October pay")
	for query, want := range map[string]int{
		"categoryId=" + food: 1, "type=income": 1, "type=expense": 2, "tag=work": 2, "tag=%23Work": 2, "tag=food": 1,
		"q=lunch": 1, "q=50%25": 1, "q=%25": 1, "q=_": 0,
	} {
		if got := getList(t, a, cookie, "month=2026-10&"+query).count(); got != want {
			t.Errorf("%s: got %d want %d", query, got, want)
		}
	}
	if l := getList(t, a, cookie, "month=2026-10&q=50%25"); l.Days[0].Transactions[0].Note != "Grab to office 50% off" {
		t.Errorf("q=50%% matched wrong row: %+v", l)
	}
	r := a.Do(t, "GET", txURL+"?type=bogus", nil, cookie)
	if r.Status != 400 || r.Field("type") != "Choose expense, income or transfer" {
		t.Fatalf("bogus type: %d %s", r.Status, r.Raw)
	}
}

func TestList_CombinedFilters_USS53AC1(t *testing.T) {
	a, u, cookie := setup(t)
	bca := apiAccount(t, a, cookie, "BCA", 0)
	cash := apiAccount(t, a, cookie, "Cash", 0)
	food := catID(t, a, u.ID, "Food & Drinks")
	transport := catID(t, a, u.ID, "Transport")
	dbTxn(t, a, u.ID, models.TypeExpense, 25000, bca, "", food, wib(2026, 10, 20, 12, 0), "")
	dbTxn(t, a, u.ID, models.TypeExpense, 15000, bca, "", food, wib(2026, 10, 20, 8, 0), "")
	dbTxn(t, a, u.ID, models.TypeExpense, 10000, bca, "", food, wib(2026, 10, 18, 9, 0), "")
	dbTxn(t, a, u.ID, models.TypeExpense, 7000, bca, "", transport, wib(2026, 10, 20, 10, 0), "")
	dbTxn(t, a, u.ID, models.TypeExpense, 9000, cash, "", food, wib(2026, 10, 20, 11, 0), "")
	dbTxn(t, a, u.ID, models.TypeExpense, 5000, bca, "", food, wib(2026, 9, 20, 12, 0), "")
	l := getList(t, a, cookie, "month=2026-10&accountId="+bca+"&categoryId="+food)
	if len(l.Days) != 2 || l.count() != 3 {
		t.Fatalf("got %+v", l)
	}
	d0, d1 := l.Days[0], l.Days[1]
	if d0.Date != "2026-10-20" || d0.NetTotal != -40000 || d0.Transactions[0].Amount != 25000 || d0.Transactions[1].Amount != 15000 ||
		d1.Date != "2026-10-18" || d1.NetTotal != -10000 {
		t.Fatalf("got %+v", l)
	}
}

func TestList_Pagination_NetTotalCoversWholeDay(t *testing.T) {
	a, u, cookie := setup(t)
	acc := apiAccount(t, a, cookie, "Cash", 0)
	for i := 0; i < 55; i++ {
		dbTxn(t, a, u.ID, models.TypeExpense, 1000, acc, "", "", wib(2026, 10, 20, 0, i), "")
	}
	p1 := getList(t, a, cookie, "month=2026-10")
	if len(p1.Days) != 1 || p1.count() != 50 || !p1.HasMore || p1.Days[0].NetTotal != -55000 || p1.Days[0].Transactions[0].Time != "00:54" {
		t.Fatalf("p1: days=%d count=%d more=%v net=%d first=%s", len(p1.Days), p1.count(), p1.HasMore, p1.Days[0].NetTotal, p1.Days[0].Transactions[0].Time)
	}
	p2 := getList(t, a, cookie, "month=2026-10&page=2")
	if len(p2.Days) != 1 || p2.count() != 5 || p2.HasMore || p2.Days[0].NetTotal != -55000 || p2.Page != 2 {
		t.Fatalf("p2: %+v", p2)
	}
	seen := map[uuid.UUID]bool{}
	for _, x := range p1.Days[0].Transactions {
		seen[x.ID] = true
	}
	for _, x := range p2.Days[0].Transactions {
		if seen[x.ID] {
			t.Fatalf("id %s on both pages", x.ID)
		}
	}
	if p3 := getList(t, a, cookie, "month=2026-10&page=3"); len(p3.Days) != 0 || p3.HasMore {
		t.Fatalf("p3: %+v", p3)
	}
	for _, q := range []string{"page=0", "page=abc"} {
		if l := getList(t, a, cookie, "month=2026-10&"+q); l.Page != 1 {
			t.Fatalf("%s: page %d", q, l.Page)
		}
	}
}

func TestList_DayGroupingSplitAcrossPages(t *testing.T) {
	a, u, cookie := setup(t)
	acc := apiAccount(t, a, cookie, "Cash", 0)
	for i := 0; i < 30; i++ {
		dbTxn(t, a, u.ID, models.TypeExpense, 100, acc, "", "", wib(2026, 10, 21, 0, i), "")
		dbTxn(t, a, u.ID, models.TypeExpense, 100, acc, "", "", wib(2026, 10, 20, 0, i), "")
	}
	p1 := getList(t, a, cookie, "month=2026-10")
	if len(p1.Days) != 2 || len(p1.Days[0].Transactions) != 30 || len(p1.Days[1].Transactions) != 20 ||
		p1.Days[0].Date != "2026-10-21" || p1.Days[0].NetTotal != -3000 ||
		p1.Days[1].Date != "2026-10-20" || p1.Days[1].NetTotal != -3000 {
		t.Fatalf("p1: %+v", p1)
	}
	p2 := getList(t, a, cookie, "month=2026-10&page=2")
	if len(p2.Days) != 1 || p2.Days[0].Date != "2026-10-20" || len(p2.Days[0].Transactions) != 10 || p2.Days[0].NetTotal != -3000 {
		t.Fatalf("p2: %+v", p2)
	}
}

func TestList_EmptyResult(t *testing.T) {
	a, _, cookie := setup(t)
	r := a.Do(t, "GET", txURL, nil, cookie)
	mustStatus(t, r, 200)
	days, ok := r.Body["days"].([]any)
	if !ok || len(days) != 0 || r.Body["hasMore"] != false || r.Body["page"] != float64(1) {
		t.Fatalf("body %s", r.Raw)
	}
}

func TestList_InvalidParams(t *testing.T) {
	a, _, cookie := setup(t)
	for _, m := range []string{"2026-13", "abc", "2026-1"} {
		r := a.Do(t, "GET", txURL+"?month="+m, nil, cookie)
		if r.Status != 400 || r.ErrCode() != "VALIDATION_ERROR" || r.Field("month") != "Enter a valid month (YYYY-MM)" {
			t.Errorf("month=%s: %d %s", m, r.Status, r.Raw)
		}
	}
	for _, q := range []string{"accountId=not-a-uuid", "categoryId=not-a-uuid"} {
		r := a.Do(t, "GET", txURL+"?"+q, nil, cookie)
		days, ok := r.Body["days"].([]any)
		if r.Status != 200 || !ok || len(days) != 0 {
			t.Errorf("%s: %d %s", q, r.Status, r.Raw)
		}
	}
}

func TestList_OtherUsersTransactionsNeverListed(t *testing.T) {
	a, u, cookie := setup(t)
	u2, cookie2 := a.NewUser(t, "u2@test.local")
	acc1 := apiAccount(t, a, cookie, "Cash", 0)
	acc2 := apiAccount(t, a, cookie2, "Cash", 0)
	dbTxn(t, a, u.ID, models.TypeExpense, 1000, acc1, "", "", wib(2026, 10, 5, 9, 0), "mine")
	other := dbTxn(t, a, u2.ID, models.TypeExpense, 2000, acc2, "", "", wib(2026, 10, 5, 9, 0), "theirs")
	l := getList(t, a, cookie, "")
	if l.count() != 1 || l.Days[0].Transactions[0].ID == other.ID {
		t.Fatalf("leak: %+v", l)
	}
	if l := getList(t, a, cookie, "accountId="+acc2); len(l.Days) != 0 {
		t.Fatalf("foreign account listed: %+v", l)
	}
	r := a.Do(t, "GET", txURL, nil, "")
	if r.Status != 401 || r.ErrCode() != "UNAUTHENTICATED" {
		t.Fatalf("no cookie: %d %s", r.Status, r.Raw)
	}
}

func TestList_DTOShape(t *testing.T) {
	a, u, cookie := setup(t)
	acc := apiAccount(t, a, cookie, "Cash", 0)
	dbTxn(t, a, u.ID, models.TypeExpense, 1000, acc, "", catID(t, a, u.ID, "Food & Drinks"), wib(2026, 10, 5, 9, 0), "")
	r := a.Do(t, "GET", txURL+"?month=2026-10", nil, cookie)
	mustStatus(t, r, 200)
	tx := r.Body["days"].([]any)[0].(map[string]any)["transactions"].([]any)[0].(map[string]any)
	if tx["account"].(map[string]any)["name"] != "Cash" || tx["category"].(map[string]any)["name"] != "Food & Drinks" ||
		!strings.HasSuffix(tx["occurredAt"].(string), "+07:00") {
		t.Fatalf("shape: %v", tx)
	}
	if _, ok := tx["tags"].([]any); !ok {
		t.Fatalf("tags not array: %v", tx["tags"])
	}
}

func getTags(t *testing.T, a *testutil.TestApp, cookie, query string) []string {
	t.Helper()
	r := a.Do(t, "GET", "/api/v1/tags"+query, nil, cookie)
	mustStatus(t, r, 200)
	var out struct {
		Tags []string `json:"tags"`
	}
	r.Decode(t, &out)
	if out.Tags == nil {
		t.Fatalf("tags is null: %s", r.Raw)
	}
	return out.Tags
}

func TestTags_PrefixAutocomplete(t *testing.T) {
	a, u, cookie := setup(t)
	acc := apiAccount(t, a, cookie, "Cash", 0)
	at := wib(2026, 10, 5, 9, 0)
	dbTxn(t, a, u.ID, models.TypeExpense, 1, acc, "", "", at, "", "food", "fun")
	dbTxn(t, a, u.ID, models.TypeExpense, 1, acc, "", "", at, "", "trip-2026", "travel")
	dbTxn(t, a, u.ID, models.TypeExpense, 1, acc, "", "", at, "", "trip-2025", "food")
	every := "food,fun,travel,trip-2025,trip-2026"
	for q, want := range map[string]string{
		"?q=t": "travel,trip-2025,trip-2026", "?q=": every, "": every, "?q=F": "food,fun",
		"?q=" + url.QueryEscape("#fo"): "food", "?q=zzz": "", "?q=_": "", "?q=%25": "",
	} {
		if got := strings.Join(getTags(t, a, cookie, q), ","); got != want {
			t.Errorf("%q: got %q want %q", q, got, want)
		}
	}
}

func TestTags_MaxTenAndScopedToUser(t *testing.T) {
	a, u, cookie := setup(t)
	u2, cookie2 := a.NewUser(t, "u2@test.local")
	acc1 := apiAccount(t, a, cookie, "Cash", 0)
	acc2 := apiAccount(t, a, cookie2, "Cash", 0)
	at := wib(2026, 10, 5, 9, 0)
	dbTxn(t, a, u.ID, models.TypeExpense, 1, acc1, "", "", at, "", "t01", "t02", "t03", "t04", "t05", "t06")
	dbTxn(t, a, u.ID, models.TypeExpense, 1, acc1, "", "", at, "", "t07", "t08", "t09", "t10", "t11", "t12")
	dbTxn(t, a, u2.ID, models.TypeExpense, 1, acc2, "", "", at, "", "secret")
	got := getTags(t, a, cookie, "")
	if len(got) != 10 || got[0] != "t01" || got[9] != "t10" {
		t.Fatalf("got %v", got)
	}
	if got := getTags(t, a, cookie2, ""); strings.Join(got, ",") != "secret" {
		t.Fatalf("u2 got %v", got)
	}
	if r := a.Do(t, "GET", "/api/v1/tags", nil, ""); r.Status != 401 {
		t.Fatalf("no cookie: %d", r.Status)
	}
}
