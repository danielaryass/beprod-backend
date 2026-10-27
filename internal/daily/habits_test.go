package daily_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"beprod/internal/models"
	"beprod/internal/testutil"
)

const habitsBase = "/api/v1/habits"

type env struct {
	a      *testutil.TestApp
	cookie string
}

func setup(t *testing.T) env {
	t.Helper()
	testutil.SetNow(t, time.Date(2026, 10, 27, 5, 0, 0, 0, time.UTC))
	a := testutil.NewApp(t)
	_, cookie := a.NewUser(t, "u@example.com")
	return env{a, cookie}
}

func validHabit(over map[string]any) map[string]any {
	b := map[string]any{"name": "Water", "kind": "count", "target": 8, "unit": "glasses", "days": []int{1, 2, 3, 4, 5}}
	for k, v := range over {
		b[k] = v
	}
	return b
}

func (e env) habit(t *testing.T, body map[string]any, want int) map[string]any {
	t.Helper()
	r := e.a.Do(t, "POST", habitsBase, body, e.cookie)
	if r.Status != want {
		t.Fatalf("create habit: %d, want %d: %s", r.Status, want, r.Raw)
	}
	h, _ := r.Body["habit"].(map[string]any)
	return h
}

func (e env) names(t *testing.T, key string) []string {
	t.Helper()
	r := e.a.Do(t, "GET", habitsBase, nil, e.cookie)
	list, ok := r.Body[key].([]any)
	if r.Status != 200 || !ok {
		t.Fatalf("list: %d %s", r.Status, r.Raw)
	}
	out := []string{}
	for _, h := range list {
		out = append(out, h.(map[string]any)["name"].(string))
	}
	return out
}

func TestHabits_CreateList(t *testing.T) {
	e := setup(t)
	h := e.habit(t, map[string]any{"name": " Water ", "kind": "count", "target": 8, "unit": "glasses", "days": []int{5, 1, 2, 3, 4}}, 201)
	want := map[string]any{"name": "Water", "kind": "count", "target": 8.0, "unit": "glasses", "days": []any{1.0, 2.0, 3.0, 4.0, 5.0}, "hidden": false}
	for k, v := range want {
		if !reflect.DeepEqual(h[k], v) {
			t.Errorf("%s = %v, want %v", k, h[k], v)
		}
	}
	c := e.habit(t, map[string]any{"name": "Read", "kind": "check", "target": 50, "unit": "pages", "days": []int{1, 2, 3, 4, 5, 6, 7}}, 201)
	if c["target"] != 1.0 || c["unit"] != "" {
		t.Errorf("check habit target/unit = %v/%v", c["target"], c["unit"])
	}
	if got := e.names(t, "habits"); !reflect.DeepEqual(got, []string{"Water", "Read"}) {
		t.Errorf("order = %v", got)
	}
	if got := e.names(t, "hidden"); len(got) != 0 {
		t.Errorf("hidden = %v", got)
	}
}

func TestHabits_Validation(t *testing.T) {
	e := setup(t)
	e.habit(t, validHabit(nil), 201)
	cases := []struct {
		name  string
		over  map[string]any
		field string
		msg   string
	}{
		{"empty name", map[string]any{"name": ""}, "name", "Name must be 1–40 characters"},
		{"long name", map[string]any{"name": strings.Repeat("a", 41)}, "name", "Name must be 1–40 characters"},
		{"bad kind", map[string]any{"kind": "timer"}, "kind", "Choose done or count"},
		{"target low", map[string]any{"kind": "count", "target": 1}, "target", "Target must be 2–999"},
		{"target high", map[string]any{"kind": "count", "target": 1000}, "target", "Target must be 2–999"},
		{"long unit", map[string]any{"kind": "count", "unit": strings.Repeat("a", 13)}, "unit", "Unit must be at most 12 characters"},
		{"no days", map[string]any{"days": []int{}}, "days", "Pick at least one day"},
		{"day zero", map[string]any{"days": []int{0}}, "days", "Pick at least one day"},
		{"repeated day", map[string]any{"days": []int{1, 1}}, "days", "Pick at least one day"},
		{"duplicate name", map[string]any{"name": "water"}, "name", "You already have a habit with this name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := e.a.Do(t, "POST", habitsBase, validHabit(c.over), e.cookie)
			if r.Status != 400 || r.Field(c.field) != c.msg {
				t.Errorf("got %d %s, want 400 %s=%q", r.Status, r.Raw, c.field, c.msg)
			}
		})
	}
}

const errLimit = "You can have up to 30 habits"

func TestHabits_Limit30(t *testing.T) {
	e := setup(t)
	var first string
	for i := 0; i < 30; i++ {
		h := e.habit(t, validHabit(map[string]any{"name": fmt.Sprintf("Habit %d", i)}), 201)
		if i == 0 {
			first = h["id"].(string)
		}
	}
	r := e.a.Do(t, "POST", habitsBase, validHabit(map[string]any{"name": "One more"}), e.cookie)
	if r.Status != 400 || r.Field("name") != errLimit {
		t.Fatalf("31st: %d %s", r.Status, r.Raw)
	}
	if r := e.a.Do(t, "PATCH", habitsBase+"/"+first, map[string]any{"hidden": true}, e.cookie); r.Status != 200 {
		t.Fatalf("hide: %d %s", r.Status, r.Raw)
	}
	e.habit(t, validHabit(map[string]any{"name": "Replacement"}), 201)
	r = e.a.Do(t, "PATCH", habitsBase+"/"+first, map[string]any{"hidden": false}, e.cookie)
	if r.Status != 400 || r.Field("name") != errLimit {
		t.Fatalf("unhide: %d %s", r.Status, r.Raw)
	}
}

func TestHabits_HiddenNameFree(t *testing.T) {
	e := setup(t)
	id := e.habit(t, validHabit(nil), 201)["id"].(string)
	r := e.a.Do(t, "PATCH", habitsBase+"/"+id, map[string]any{"hidden": true}, e.cookie)
	if r.Status != 200 || r.Body["habit"].(map[string]any)["hidden"] != true {
		t.Fatalf("hide: %d %s", r.Status, r.Raw)
	}
	if got := e.names(t, "hidden"); !reflect.DeepEqual(got, []string{"Water"}) {
		t.Errorf("hidden = %v", got)
	}
	e.habit(t, validHabit(nil), 201)
}

func TestHabits_UnhideClash(t *testing.T) {
	e := setup(t)
	id := e.habit(t, validHabit(nil), 201)["id"].(string)
	e.a.Do(t, "PATCH", habitsBase+"/"+id, map[string]any{"hidden": true}, e.cookie)
	e.habit(t, validHabit(nil), 201)
	r := e.a.Do(t, "PATCH", habitsBase+"/"+id, map[string]any{"hidden": false}, e.cookie)
	if r.Status != 400 || r.Field("name") != "You already have a habit with this name" {
		t.Fatalf("unhide: %d %s", r.Status, r.Raw)
	}
}

func TestHabits_PatchKeepsKind(t *testing.T) {
	e := setup(t)
	id := e.habit(t, validHabit(map[string]any{"kind": "check"}), 201)["id"].(string)
	r := e.a.Do(t, "PATCH", habitsBase+"/"+id, map[string]any{"kind": "count", "target": 5, "days": []int{6, 7}}, e.cookie)
	h, _ := r.Body["habit"].(map[string]any)
	if r.Status != 200 || h["kind"] != "check" || h["target"] != 1.0 || !reflect.DeepEqual(h["days"], []any{6.0, 7.0}) {
		t.Fatalf("patch: %d %s", r.Status, r.Raw)
	}
}

func TestHabits_Order(t *testing.T) {
	e := setup(t)
	ids := map[string]string{}
	for _, n := range []string{"A", "B", "C"} {
		ids[n] = e.habit(t, validHabit(map[string]any{"name": n}), 201)["id"].(string)
	}
	put := func(body ...string) int {
		return e.a.Do(t, "PUT", habitsBase+"/order", map[string]any{"ids": body}, e.cookie).Status
	}
	if s := put(ids["C"], ids["A"], ids["B"]); s != 204 {
		t.Fatalf("order: %d", s)
	}
	if got := e.names(t, "habits"); !reflect.DeepEqual(got, []string{"C", "A", "B"}) {
		t.Errorf("order = %v", got)
	}
	for _, body := range [][]string{{ids["C"], ids["A"]}, {ids["C"], ids["A"], ids["A"]}} {
		r := e.a.Do(t, "PUT", habitsBase+"/order", map[string]any{"ids": body}, e.cookie)
		if r.Status != 400 || r.ErrCode() != "INVALID_ORDER" {
			t.Errorf("bad order %v: %d %s", body, r.Status, r.Raw)
		}
	}
}

func TestHabits_UnhideGoesLast(t *testing.T) {
	e := setup(t)
	ids := map[string]string{}
	for _, n := range []string{"A", "B", "C"} {
		ids[n] = e.habit(t, validHabit(map[string]any{"name": n}), 201)["id"].(string)
	}
	e.a.Do(t, "PATCH", habitsBase+"/"+ids["A"], map[string]any{"hidden": true}, e.cookie)
	if r := e.a.Do(t, "PATCH", habitsBase+"/"+ids["A"], map[string]any{"hidden": false}, e.cookie); r.Status != 200 {
		t.Fatalf("unhide: %d %s", r.Status, r.Raw)
	}
	if got := e.names(t, "habits"); !reflect.DeepEqual(got, []string{"B", "C", "A"}) {
		t.Errorf("order = %v", got)
	}
}

func TestHabits_DeleteRemovesLogs(t *testing.T) {
	e := setup(t)
	id := e.habit(t, validHabit(nil), 201)["id"].(string)
	if r := e.a.Do(t, "PUT", "/api/v1/daily/2026-10-27/habits/"+id, map[string]any{"value": 1}, e.cookie); r.Status != 200 {
		t.Fatalf("log: %d %s", r.Status, r.Raw)
	}
	if r := e.a.Do(t, "DELETE", habitsBase+"/"+id, nil, e.cookie); r.Status != 204 {
		t.Fatalf("delete: %d %s", r.Status, r.Raw)
	}
	var n int64
	e.a.DB.Model(&models.HabitLog{}).Where("habit_id = ?", id).Count(&n)
	if n != 0 {
		t.Errorf("logs left: %d", n)
	}
	if r := e.a.Do(t, "PATCH", habitsBase+"/"+id, map[string]any{"name": "X"}, e.cookie); r.Status != 404 {
		t.Errorf("patch deleted: %d", r.Status)
	}
}
