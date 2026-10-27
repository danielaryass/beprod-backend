package categories_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/lib/pq"

	"beprod/internal/categories"
	"beprod/internal/jakarta"
	"beprod/internal/models"
	"beprod/internal/testutil"
)

func apiCategory(t *testing.T, a *testutil.TestApp, cookie, name, typ, icon, color string) categories.DTO {
	t.Helper()
	r := a.Do(t, "POST", "/api/v1/categories", map[string]any{"name": name, "type": typ, "icon": icon, "color": color}, cookie)
	if r.Status != 201 {
		t.Fatalf("create %q: status %d %s", name, r.Status, r.Raw)
	}
	var out struct{ Category categories.DTO }
	r.Decode(t, &out)
	return out.Category
}

func catByName(t *testing.T, a *testutil.TestApp, userID uuid.UUID, name, typ string) models.Category {
	t.Helper()
	var c models.Category
	if err := a.DB.Where("user_id = ? AND name = ? AND type = ?", userID, name, typ).First(&c).Error; err != nil {
		t.Fatalf("catByName %q: %v", name, err)
	}
	return c
}

func errMessage(r testutil.Response) string {
	e, _ := r.Body["error"].(map[string]any)
	s, _ := e["message"].(string)
	return s
}

func insertTx(t *testing.T, a *testutil.TestApp, userID, catID uuid.UUID) {
	t.Helper()
	err := a.DB.Create(&models.Transaction{UserID: userID, Type: models.TypeExpense, Amount: 1000, OccurredAt: jakarta.Now(),
		AccountID: uuid.New(), CategoryID: &catID, Tags: pq.StringArray{}, Source: models.SourceApp}).Error
	if err != nil {
		t.Fatal(err)
	}
}

func insertBudget(t *testing.T, a *testutil.TestApp, userID, catID uuid.UUID) {
	t.Helper()
	if err := a.DB.Create(&models.Budget{UserID: userID, CategoryID: catID}).Error; err != nil {
		t.Fatal(err)
	}
}

func insertRule(t *testing.T, a *testutil.TestApp, userID, catID uuid.UUID, status, reason string) models.RecurringRule {
	t.Helper()
	r := models.RecurringRule{UserID: userID, Type: models.TypeExpense, Amount: 1000, AccountID: uuid.New(), CategoryID: &catID,
		Tags: pq.StringArray{}, Frequency: "monthly", StartDate: jakarta.Today(), NextDueDate: jakarta.Today(),
		Status: status, PausedReason: reason}
	if err := a.DB.Create(&r).Error; err != nil {
		t.Fatal(err)
	}
	return r
}

func catPath(id uuid.UUID, suffix string) string { return "/api/v1/categories/" + id.String() + suffix }

func listBody(t *testing.T, a *testutil.TestApp, cookie, query string) (active, archived []categories.DTO) {
	t.Helper()
	r := a.Do(t, "GET", "/api/v1/categories"+query, nil, cookie)
	if r.Status != 200 {
		t.Fatalf("list: %d %s", r.Status, r.Raw)
	}
	var out struct{ Categories, Archived []categories.DTO }
	r.Decode(t, &out)
	return out.Categories, out.Archived
}

func TestList_ReturnsSeeded14(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "u1@test.local")
	r := a.Do(t, "GET", "/api/v1/categories", nil, cookie)
	if r.Status != 200 {
		t.Fatalf("status %d", r.Status)
	}
	active, _ := listBody(t, a, cookie, "")
	if len(active) != 14 {
		t.Fatalf("want 14, got %d", len(active))
	}
	for i, c := range active {
		want := "expense"
		if i >= 9 {
			want = "income"
		}
		if c.Type != want {
			t.Errorf("item %d type %s want %s", i, c.Type, want)
		}
	}
	if active[0].Name != "Food & Drinks" || active[0].SortOrder != 1 {
		t.Errorf("first = %+v", active[0])
	}
	if arch, ok := r.Body["archived"].([]any); !ok || len(arch) != 0 {
		t.Errorf("archived should be empty array, got %#v", r.Body["archived"])
	}
	if inc, _ := listBody(t, a, cookie, "?type=income"); len(inc) != 5 {
		t.Errorf("income: %d", len(inc))
	}
	r = a.Do(t, "GET", "/api/v1/categories?type=foo", nil, cookie)
	if r.Status != 400 || r.Field("type") != "Choose income or expense" {
		t.Errorf("bad type: %d %s", r.Status, r.Raw)
	}
}

func TestMeta(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "u1@test.local")
	r := a.Do(t, "GET", "/api/v1/categories/meta", nil, cookie)
	icons, _ := r.Body["icons"].([]any)
	colors, _ := r.Body["colors"].([]any)
	if r.Status != 200 || len(icons) != 24 || len(colors) != 12 {
		t.Errorf("meta: %d icons=%d colors=%d", r.Status, len(icons), len(colors))
	}
}

func TestCreate_Coffee(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "u1@test.local")
	c := apiCategory(t, a, cookie, "Coffee", "expense", "coffee", "amber")
	if c.SortOrder != 10 || c.Status != "active" || c.Type != "expense" {
		t.Errorf("got %+v", c)
	}
	if exp, _ := listBody(t, a, cookie, "?type=expense"); len(exp) != 10 {
		t.Errorf("expense: %d", len(exp))
	}
}

func TestCreate_DuplicateName(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "u1@test.local")
	_, cookie2 := a.NewUser(t, "u2@test.local")
	apiCategory(t, a, cookie, "Coffee", "expense", "coffee", "amber")
	body := func(n, typ string) map[string]any {
		return map[string]any{"name": n, "type": typ, "icon": "coffee", "color": "amber"}
	}
	r := a.Do(t, "POST", "/api/v1/categories", body("coffee", "expense"), cookie)
	if r.Status != 409 || r.ErrCode() != "CATEGORY_NAME_TAKEN" || errMessage(r) != "This category already exists." {
		t.Errorf("dup: %d %s", r.Status, r.Raw)
	}
	if c := apiCategory(t, a, cookie, "Coffee", "income", "coffee", "amber"); c.SortOrder != 6 {
		t.Errorf("income sortOrder %d", c.SortOrder)
	}
	if r := a.Do(t, "POST", "/api/v1/categories", body("food & drinks", "expense"), cookie); r.Status != 409 {
		t.Errorf("seed dup: %d", r.Status)
	}
	apiCategory(t, a, cookie2, "Coffee", "expense", "coffee", "amber")
}

func TestCreate_Validation(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "u1@test.local")
	post := func(n, typ, icon, color string) testutil.Response {
		return a.Do(t, "POST", "/api/v1/categories", map[string]any{"name": n, "type": typ, "icon": icon, "color": color}, cookie)
	}
	check := func(r testutil.Response, field, msg string) {
		t.Helper()
		if r.Status != 400 || r.ErrCode() != "VALIDATION_ERROR" || r.Field(field) != msg {
			t.Errorf("%s: %d %s", field, r.Status, r.Raw)
		}
	}
	check(post("X", "expense", "rocket", "amber"), "icon", "Choose an icon")
	check(post("X", "expense", "tag", "black"), "color", "Choose a colour")
	check(post("X", "transfer", "tag", "amber"), "type", "Choose income or expense")
	check(post("", "expense", "tag", "amber"), "name", "Name must be 1–30 characters")
	check(post(strings.Repeat("a", 31), "expense", "tag", "amber"), "name", "Name must be 1–30 characters")
	r := post("", "x", "x", "x")
	if r.Field("name") == "" || r.Field("type") == "" || r.Field("icon") == "" || r.Field("color") == "" {
		t.Errorf("want all four fields: %s", r.Raw)
	}
	if r := post(strings.Repeat("a", 30), "expense", "tag", "amber"); r.Status != 201 {
		t.Errorf("30 chars: %d", r.Status)
	}
}

func TestCreate_ActiveLimitPerType(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	for i := 1; i <= 91; i++ {
		c := models.Category{UserID: u.ID, Name: fmt.Sprintf("Cat %d", i), Type: models.TypeExpense, Icon: "tag", Color: "red",
			SortOrder: 9 + i, Status: models.StatusActive}
		if err := a.DB.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
	}
	body := map[string]any{"name": "Extra", "type": "expense", "icon": "tag", "color": "red"}
	r := a.Do(t, "POST", "/api/v1/categories", body, cookie)
	if r.Status != 400 || r.ErrCode() != "CATEGORY_LIMIT" || errMessage(r) != "You can have at most 100 active categories of this type." {
		t.Errorf("limit: %d %s", r.Status, r.Raw)
	}
	apiCategory(t, a, cookie, "Extra", "income", "tag", "red")
	a.DB.Model(&models.Category{}).Where("user_id = ? AND name = ?", u.ID, "Cat 1").Update("status", models.StatusArchived)
	if r := a.Do(t, "POST", "/api/v1/categories", body, cookie); r.Status != 201 {
		t.Errorf("after archive: %d %s", r.Status, r.Raw)
	}
}

func TestUpdate_RenameIconColor_IgnoresType(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "u1@test.local")
	c := apiCategory(t, a, cookie, "Coffee", "expense", "coffee", "amber")
	r := a.Do(t, "PATCH", catPath(c.ID, ""), map[string]any{"name": "Cafe", "icon": "tag", "color": "blue", "type": "income"}, cookie)
	var out struct{ Category categories.DTO }
	r.Decode(t, &out)
	got := out.Category
	if r.Status != 200 || got.Name != "Cafe" || got.Icon != "tag" || got.Color != "blue" || got.Type != "expense" || got.SortOrder != c.SortOrder {
		t.Errorf("patch: %d %s", r.Status, r.Raw)
	}
	if r := a.Do(t, "PATCH", catPath(c.ID, ""), map[string]any{}, cookie); r.Status != 200 {
		t.Errorf("empty: %d", r.Status)
	}
	if r := a.Do(t, "PATCH", catPath(c.ID, ""), map[string]any{"icon": "x"}, cookie); r.Status != 400 || r.Field("icon") == "" {
		t.Errorf("icon: %d %s", r.Status, r.Raw)
	}
	if r := a.Do(t, "PATCH", catPath(c.ID, ""), map[string]any{"name": "Transport"}, cookie); r.Status != 409 {
		t.Errorf("dup: %d", r.Status)
	}
}

func TestDelete_Unused_HardDeletes(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	c := apiCategory(t, a, cookie, "Coffee", "expense", "coffee", "amber")
	r := a.Do(t, "DELETE", catPath(c.ID, ""), nil, cookie)
	if r.Status != 200 || r.Body["result"] != "deleted" {
		t.Errorf("delete: %d %s", r.Status, r.Raw)
	}
	var n int64
	a.DB.Model(&models.Category{}).Where("user_id = ? AND name = ?", u.ID, "Coffee").Count(&n)
	if n != 0 {
		t.Errorf("row still present")
	}
}

func TestDelete_WithTransaction_Archives(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	c := apiCategory(t, a, cookie, "Coffee", "expense", "coffee", "amber")
	insertTx(t, a, u.ID, c.ID)
	r := a.Do(t, "DELETE", catPath(c.ID, ""), nil, cookie)
	if r.Status != 200 || r.Body["result"] != "archived" || r.Body["pausedRules"] != float64(0) || r.Body["budgetDeactivated"] != false {
		t.Errorf("delete: %d %s", r.Status, r.Raw)
	}
	active, archived := listBody(t, a, cookie, "")
	for _, x := range active {
		if x.ID == c.ID {
			t.Errorf("still active")
		}
	}
	if len(archived) != 1 || archived[0].ID != c.ID {
		t.Errorf("archived = %+v", archived)
	}
}

func TestDelete_WithBudget_DeactivatesBudget(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	c := apiCategory(t, a, cookie, "Coffee", "expense", "coffee", "amber")
	insertBudget(t, a, u.ID, c.ID)
	r := a.Do(t, "DELETE", catPath(c.ID, ""), nil, cookie)
	if r.Status != 200 || r.Body["result"] != "archived" || r.Body["budgetDeactivated"] != true {
		t.Errorf("delete: %d %s", r.Status, r.Raw)
	}
	var b models.Budget
	a.DB.Where("category_id = ?", c.ID).First(&b)
	if b.Status != models.StatusInactive {
		t.Errorf("budget status %s", b.Status)
	}
	// already archived + budget already inactive
	r = a.Do(t, "DELETE", catPath(c.ID, ""), nil, cookie)
	if r.Body["result"] != "archived" || r.Body["budgetDeactivated"] != false || r.Body["pausedRules"] != float64(0) {
		t.Errorf("re-delete: %s", r.Raw)
	}
}

func TestDelete_PausesActiveRules(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	c := apiCategory(t, a, cookie, "Coffee", "expense", "coffee", "amber")
	other := apiCategory(t, a, cookie, "Tea", "expense", "coffee", "amber")
	ra := insertRule(t, a, u.ID, c.ID, models.StatusActive, "")
	rb := insertRule(t, a, u.ID, c.ID, models.StatusActive, "")
	rc := insertRule(t, a, u.ID, c.ID, models.RulePaused, "user")
	rd := insertRule(t, a, u.ID, other.ID, models.StatusActive, "")
	r := a.Do(t, "DELETE", catPath(c.ID, ""), nil, cookie)
	if r.Status != 200 || r.Body["result"] != "archived" || r.Body["pausedRules"] != float64(2) {
		t.Fatalf("delete: %d %s", r.Status, r.Raw)
	}
	state := func(id uuid.UUID) (string, string) {
		var x models.RecurringRule
		a.DB.First(&x, "id = ?", id)
		return x.Status, x.PausedReason
	}
	for _, id := range []uuid.UUID{ra.ID, rb.ID} {
		if s, p := state(id); s != "paused" || p != "category_archived" {
			t.Errorf("rule %s: %s/%s", id, s, p)
		}
	}
	if s, p := state(rc.ID); s != "paused" || p != "user" {
		t.Errorf("C: %s/%s", s, p)
	}
	if s, _ := state(rd.ID); s != "active" {
		t.Errorf("D: %s", s)
	}
}

func TestDeletePreview(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	preview := func(id uuid.UUID) map[string]any {
		r := a.Do(t, "GET", catPath(id, "/delete-preview"), nil, cookie)
		if r.Status != 200 {
			t.Fatalf("preview: %d %s", r.Status, r.Raw)
		}
		return r.Body
	}
	empty := apiCategory(t, a, cookie, "Empty", "expense", "tag", "red")
	if b := preview(empty.ID); b["willArchive"] != false || b["hasTransactions"] != false || b["hasBudget"] != false || b["activeRecurringRules"] != float64(0) {
		t.Errorf("empty: %v", b)
	}
	full := apiCategory(t, a, cookie, "Full", "expense", "tag", "red")
	insertTx(t, a, u.ID, full.ID)
	insertBudget(t, a, u.ID, full.ID)
	insertRule(t, a, u.ID, full.ID, models.StatusActive, "")
	insertRule(t, a, u.ID, full.ID, models.StatusActive, "")
	insertRule(t, a, u.ID, full.ID, models.RulePaused, "user")
	if b := preview(full.ID); b["willArchive"] != true || b["hasTransactions"] != true || b["hasBudget"] != true || b["activeRecurringRules"] != float64(2) {
		t.Errorf("full: %v", b)
	}
	bo := apiCategory(t, a, cookie, "BudgetOnly", "expense", "tag", "red")
	insertBudget(t, a, u.ID, bo.ID)
	if b := preview(bo.ID); b["willArchive"] != true || b["hasBudget"] != true || b["hasTransactions"] != false {
		t.Errorf("budget only: %v", b)
	}
}

func TestUnarchive_OK_BudgetStaysInactive(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	c := apiCategory(t, a, cookie, "Coffee", "expense", "coffee", "amber")
	insertBudget(t, a, u.ID, c.ID)
	rule := insertRule(t, a, u.ID, c.ID, models.StatusActive, "")
	a.Do(t, "DELETE", catPath(c.ID, ""), nil, cookie)
	r := a.Do(t, "POST", catPath(c.ID, "/unarchive"), nil, cookie)
	var out struct{ Category categories.DTO }
	r.Decode(t, &out)
	if r.Status != 200 || out.Category.Status != "active" || out.Category.SortOrder != 10 {
		t.Errorf("unarchive: %d %s", r.Status, r.Raw)
	}
	var b models.Budget
	a.DB.Where("category_id = ?", c.ID).First(&b)
	var ru models.RecurringRule
	a.DB.First(&ru, "id = ?", rule.ID)
	if b.Status != models.StatusInactive || ru.Status != models.RulePaused {
		t.Errorf("budget %s rule %s", b.Status, ru.Status)
	}
}

func TestUnarchive_NameConflict(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	c := apiCategory(t, a, cookie, "Coffee", "expense", "coffee", "amber")
	insertTx(t, a, u.ID, c.ID)
	insertBudget(t, a, u.ID, c.ID)
	a.Do(t, "DELETE", catPath(c.ID, ""), nil, cookie)
	apiCategory(t, a, cookie, "coffee", "expense", "coffee", "amber")
	r := a.Do(t, "POST", catPath(c.ID, "/unarchive"), nil, cookie)
	if r.Status != 409 || r.ErrCode() != "CATEGORY_NAME_TAKEN" ||
		errMessage(r) != "Rename this category first: an active category already uses this name." {
		t.Errorf("conflict: %d %s", r.Status, r.Raw)
	}
	old := catByName(t, a, u.ID, "Coffee", "expense")
	var b models.Budget
	a.DB.Where("category_id = ?", c.ID).First(&b)
	if old.Status != models.StatusArchived || b.Status != models.StatusInactive {
		t.Errorf("cat %s budget %s", old.Status, b.Status)
	}
}

func TestUnarchive_NotArchived_And_Limit(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	c := apiCategory(t, a, cookie, "Coffee", "expense", "coffee", "amber")
	r := a.Do(t, "POST", catPath(c.ID, "/unarchive"), nil, cookie)
	if r.Status != 400 || r.ErrCode() != "NOT_ARCHIVED" {
		t.Errorf("not archived: %d %s", r.Status, r.Raw)
	}
	for i := 1; i <= 90; i++ { // 9 seed + Coffee + 90 = 100 active
		cat := models.Category{UserID: u.ID, Name: fmt.Sprintf("Cat %d", i), Type: models.TypeExpense, Icon: "tag", Color: "red",
			SortOrder: 10 + i, Status: models.StatusActive}
		if err := a.DB.Create(&cat).Error; err != nil {
			t.Fatal(err)
		}
	}
	arch := models.Category{UserID: u.ID, Name: "Old", Type: models.TypeExpense, Icon: "tag", Color: "red", Status: models.StatusArchived}
	if err := a.DB.Create(&arch).Error; err != nil {
		t.Fatal(err)
	}
	r = a.Do(t, "POST", catPath(arch.ID, "/unarchive"), nil, cookie)
	if r.Status != 400 || r.ErrCode() != "CATEGORY_LIMIT" {
		t.Errorf("limit: %d %s", r.Status, r.Raw)
	}
}

func TestReorder(t *testing.T) {
	a := testutil.NewApp(t)
	u, cookie := a.NewUser(t, "u1@test.local")
	_, cookie2 := a.NewUser(t, "u2@test.local")
	exp, _ := listBody(t, a, cookie, "?type=expense")
	inc, _ := listBody(t, a, cookie, "?type=income")
	ids := make([]string, len(exp))
	for i, c := range exp {
		ids[len(exp)-1-i] = c.ID.String()
	}
	r := a.Do(t, "PUT", "/api/v1/categories/order", map[string]any{"type": "expense", "ids": ids}, cookie)
	if r.Status != 204 {
		t.Fatalf("reorder: %d %s", r.Status, r.Raw)
	}
	got, _ := listBody(t, a, cookie, "?type=expense")
	for i, c := range got {
		if c.ID.String() != ids[i] || c.SortOrder != i+1 {
			t.Errorf("pos %d: %+v", i, c)
		}
	}
	inc2, _ := listBody(t, a, cookie, "?type=income")
	for i := range inc {
		if inc[i] != inc2[i] {
			t.Errorf("income changed")
		}
	}
	other, _ := listBody(t, a, cookie2, "?type=expense")
	_ = u
	bad := map[string]any{
		"missing":   append([]string{}, ids[1:]...),
		"duplicate": append(append([]string{}, ids[1:]...), ids[1]),
		"income":    append(append([]string{}, ids[1:]...), inc[0].ID.String()),
		"other":     append(append([]string{}, ids[1:]...), other[0].ID.String()),
		"notuuid":   append(append([]string{}, ids[1:]...), "nope"),
	}
	for name, v := range bad {
		r := a.Do(t, "PUT", "/api/v1/categories/order", map[string]any{"type": "expense", "ids": v}, cookie)
		if r.Status != 400 || r.ErrCode() != "INVALID_ORDER" ||
			errMessage(r) != "The list must contain each active category of this type exactly once" {
			t.Errorf("%s: %d %s", name, r.Status, r.Raw)
		}
	}
	if r := a.Do(t, "PUT", "/api/v1/categories/order", map[string]any{"type": "expense"}, cookie); r.ErrCode() != "INVALID_ORDER" {
		t.Errorf("no ids: %s", r.Raw)
	}
	if r := a.Do(t, "PUT", "/api/v1/categories/order", map[string]any{"type": "foo", "ids": ids}, cookie); r.Status != 400 || r.Field("type") == "" {
		t.Errorf("type: %d %s", r.Status, r.Raw)
	}
	after, _ := listBody(t, a, cookie, "?type=expense")
	for i, c := range after {
		if c.ID.String() != ids[i] {
			t.Errorf("DB changed by invalid request")
		}
	}
}

func TestOtherUser_Gets404(t *testing.T) {
	a := testutil.NewApp(t)
	_, cookie1 := a.NewUser(t, "u1@test.local")
	_, cookie2 := a.NewUser(t, "u2@test.local")
	c := apiCategory(t, a, cookie1, "Coffee", "expense", "coffee", "amber")
	calls := [][2]string{{"PATCH", ""}, {"GET", "/delete-preview"}, {"DELETE", ""}, {"POST", "/unarchive"}}
	for _, call := range calls {
		var body any
		if call[0] == "PATCH" {
			body = map[string]any{"name": "Hacked"}
		}
		r := a.Do(t, call[0], catPath(c.ID, call[1]), body, cookie2)
		if r.Status != 404 || r.ErrCode() != "NOT_FOUND" {
			t.Errorf("%s %s: %d %s", call[0], call[1], r.Status, r.Raw)
		}
	}
	got, _ := listBody(t, a, cookie1, "?type=expense")
	if got[9].Name != "Coffee" || got[9].Status != "active" {
		t.Errorf("changed: %+v", got[9])
	}
	if list, _ := listBody(t, a, cookie2, ""); len(list) != 14 {
		t.Errorf("user2 sees %d", len(list))
	}
	ids := []string{c.ID.String()}
	exp2, _ := listBody(t, a, cookie2, "?type=expense")
	for _, x := range exp2[1:] {
		ids = append(ids, x.ID.String())
	}
	r := a.Do(t, "PUT", "/api/v1/categories/order", map[string]any{"type": "expense", "ids": ids}, cookie2)
	if r.Status != 400 || r.ErrCode() != "INVALID_ORDER" {
		t.Errorf("order: %d %s", r.Status, r.Raw)
	}
}

func TestCategories_RequireLogin(t *testing.T) {
	a := testutil.NewApp(t)
	r := a.Do(t, "GET", "/api/v1/categories", nil, "")
	if r.Status != 401 || r.ErrCode() != "UNAUTHENTICATED" {
		t.Errorf("%d %s", r.Status, r.Raw)
	}
}
