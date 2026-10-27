package daily_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"beprod/internal/models"
)

const dailyBase = "/api/v1/daily"

func allDays() []int { return []int{1, 2, 3, 4, 5, 6, 7} }

func (e env) dayNames(t *testing.T, query string) (string, []string) {
	t.Helper()
	r := e.a.Do(t, "GET", dailyBase+query, nil, e.cookie)
	list, ok := r.Body["habits"].([]any)
	if r.Status != 200 || !ok {
		t.Fatalf("daily: %d %s", r.Status, r.Raw)
	}
	out := []string{}
	for _, h := range list {
		out = append(out, h.(map[string]any)["name"].(string))
	}
	date, _ := r.Body["date"].(string)
	return date, out
}

func (e env) backdate(t *testing.T, id string, date time.Time) {
	t.Helper()
	if err := e.a.DB.Model(&models.Habit{}).Where("id = ?", id).Update("created_at", date).Error; err != nil {
		t.Fatal(err)
	}
}

func (e env) putHabit(t *testing.T, date, id string, value any) (int, map[string]any, string) {
	t.Helper()
	r := e.a.Do(t, "PUT", dailyBase+"/"+date+"/habits/"+id, map[string]any{"value": value}, e.cookie)
	h, _ := r.Body["habit"].(map[string]any)
	return r.Status, h, r.Field("value")
}

func TestDaily_DueFilter(t *testing.T) {
	e := setup(t)
	gym := e.habit(t, validHabit(map[string]any{"name": "Gym", "kind": "check", "days": []int{1, 3, 5}}), 201)["id"].(string)
	e.habit(t, validHabit(map[string]any{"name": "Read", "kind": "check", "days": allDays()}), 201)
	old := e.habit(t, validHabit(map[string]any{"name": "Old", "kind": "check", "days": allDays()}), 201)["id"].(string)
	if r := e.a.Do(t, "PATCH", habitsBase+"/"+old, map[string]any{"hidden": true}, e.cookie); r.Status != 200 {
		t.Fatalf("hide: %d", r.Status)
	}
	if _, got := e.dayNames(t, ""); !reflect.DeepEqual(got, []string{"Read"}) {
		t.Errorf("today = %v", got)
	}
	date, got := e.dayNames(t, "?date=2026-10-26")
	if date != "2026-10-26" || len(got) != 0 {
		t.Errorf("26th = %q %v", date, got)
	}
	e.backdate(t, gym, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	if _, got := e.dayNames(t, "?date=2026-10-26"); !reflect.DeepEqual(got, []string{"Gym"}) {
		t.Errorf("26th after backdate = %v", got)
	}
}

func TestDaily_LogCheckAndCount(t *testing.T) {
	e := setup(t)
	chk := e.habit(t, validHabit(map[string]any{"name": "Read", "kind": "check", "days": allDays()}), 201)["id"].(string)
	st, h, _ := e.putHabit(t, "2026-10-27", chk, 1)
	if st != 200 || h["value"] != 1.0 || h["done"] != true || h["streak"] != 1.0 {
		t.Errorf("check: %d %v", st, h)
	}
	if st, _, f := e.putHabit(t, "2026-10-27", chk, 2); st != 400 || f != "Value must be 0 or 1" {
		t.Errorf("check 2: %d %q", st, f)
	}
	cnt := e.habit(t, validHabit(map[string]any{"name": "Water", "target": 8, "days": allDays()}), 201)["id"].(string)
	_, h, _ = e.putHabit(t, "2026-10-27", cnt, 3)
	if h["done"] != false || h["streak"] != 0.0 || h["value"] != 3.0 {
		t.Errorf("count 3: %v", h)
	}
	_, h, _ = e.putHabit(t, "2026-10-27", cnt, 8)
	if h["done"] != true {
		t.Errorf("count 8: %v", h)
	}
	if st, _, f := e.putHabit(t, "2026-10-27", cnt, 10000); st != 400 || f != "Value must be 0–9999" {
		t.Errorf("count 10000: %d %q", st, f)
	}
	_, h, _ = e.putHabit(t, "2026-10-27", cnt, 0)
	if h["value"] != 0.0 || h["done"] != false {
		t.Errorf("count 0: %v", h)
	}
	var n int64
	e.a.DB.Model(&models.HabitLog{}).Where("habit_id = ?", cnt).Count(&n)
	if n != 0 {
		t.Errorf("rows left: %d", n)
	}
	if r := e.a.Do(t, "PUT", dailyBase+"/2026-10-27/habits/00000000-0000-0000-0000-000000000000", map[string]any{"value": 1}, e.cookie); r.Status != 404 {
		t.Errorf("unknown habit: %d", r.Status)
	}
}

func TestDaily_LogUpsertsOneRow(t *testing.T) {
	e := setup(t)
	id := e.habit(t, validHabit(map[string]any{"days": allDays()}), 201)["id"].(string)
	for _, v := range []int{3, 5, 4} {
		if st, _, _ := e.putHabit(t, "2026-10-27", id, v); st != 200 {
			t.Fatalf("put %d: %d", v, st)
		}
	}
	var logs []models.HabitLog
	e.a.DB.Where("habit_id = ?", id).Find(&logs)
	if len(logs) != 1 || logs[0].Value != 4 {
		t.Errorf("logs = %+v", logs)
	}
}

func TestDaily_StreakInResponse(t *testing.T) {
	e := setup(t)
	id := e.habit(t, validHabit(map[string]any{"name": "Read", "kind": "check", "days": allDays()}), 201)["id"].(string)
	e.backdate(t, id, time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC))
	streak := func() float64 {
		r := e.a.Do(t, "GET", dailyBase, nil, e.cookie)
		return r.Body["habits"].([]any)[0].(map[string]any)["streak"].(float64)
	}
	e.putHabit(t, "2026-10-25", id, 1)
	e.putHabit(t, "2026-10-26", id, 1)
	if s := streak(); s != 2 {
		t.Errorf("streak = %v, want 2", s)
	}
	e.putHabit(t, "2026-10-27", id, 1)
	if s := streak(); s != 3 {
		t.Errorf("streak = %v, want 3", s)
	}
}

func (e env) putNote(t *testing.T, date, id string, body map[string]any) (int, map[string]any, map[string]any) {
	t.Helper()
	r := e.a.Do(t, "PUT", dailyBase+"/"+date+"/notes/"+id, body, e.cookie)
	p, _ := r.Body["prompt"].(map[string]any)
	return r.Status, p, r.Body
}

func TestDaily_Notes(t *testing.T) {
	e := setup(t)
	tp := e.prompt(t, "Highlight", "text", 201)["id"].(string)
	rp := e.prompt(t, "Mood", "rating", 201)["id"].(string)
	_, p, _ := e.putNote(t, "2026-10-26", tp, map[string]any{"text": "  Coffee with Rani  "})
	if p["text"] != "Coffee with Rani" {
		t.Errorf("text = %v", p["text"])
	}
	_, p, _ = e.putNote(t, "2026-10-26", rp, map[string]any{"rating": 4})
	if p["rating"] != 4.0 {
		t.Errorf("rating = %v", p["rating"])
	}
	r := e.a.Do(t, "GET", dailyBase+"?date=2026-10-26", nil, e.cookie)
	ps := r.Body["prompts"].([]any)
	if ps[0].(map[string]any)["text"] != "Coffee with Rani" || ps[1].(map[string]any)["rating"] != 4.0 {
		t.Errorf("26th prompts = %v", ps)
	}
	r = e.a.Do(t, "GET", dailyBase, nil, e.cookie)
	for _, x := range r.Body["prompts"].([]any) {
		m := x.(map[string]any)
		if m["text"] != nil || m["rating"] != nil {
			t.Errorf("27th leaked: %v", m)
		}
	}
	_, p, _ = e.putNote(t, "2026-10-26", tp, map[string]any{"text": "   "})
	if p["text"] != nil {
		t.Errorf("blank text = %v", p["text"])
	}
	var n int64
	e.a.DB.Model(&models.NoteEntry{}).Where("prompt_id = ?", tp).Count(&n)
	if n != 0 {
		t.Errorf("text row left: %d", n)
	}
	r = e.a.Do(t, "PUT", dailyBase+"/2026-10-26/notes/"+tp, map[string]any{"text": strings.Repeat("a", 2001)}, e.cookie)
	if r.Status != 400 || r.Field("text") != "Keep it under 2000 characters" {
		t.Errorf("long: %d %s", r.Status, r.Raw)
	}
	r = e.a.Do(t, "PUT", dailyBase+"/2026-10-26/notes/"+rp, map[string]any{"rating": 6}, e.cookie)
	if r.Status != 400 || r.Field("rating") != "Pick 1–5" {
		t.Errorf("rating 6: %d %s", r.Status, r.Raw)
	}
	_, p, _ = e.putNote(t, "2026-10-26", rp, map[string]any{"rating": nil})
	if p["rating"] != nil {
		t.Errorf("null rating = %v", p["rating"])
	}
	e.a.DB.Model(&models.NoteEntry{}).Where("prompt_id = ?", rp).Count(&n)
	if n != 0 {
		t.Errorf("rating row left: %d", n)
	}
}

func TestDaily_NoteRequiresKindField(t *testing.T) {
	e := setup(t)
	tp := e.prompt(t, "Highlight", "text", 201)["id"].(string)
	rp := e.prompt(t, "Mood", "rating", 201)["id"].(string)
	e.putNote(t, "2026-10-26", tp, map[string]any{"text": "Kept"})
	e.putNote(t, "2026-10-26", rp, map[string]any{"rating": 4})
	for _, c := range []struct {
		id, field, msg string
		body           map[string]any
	}{
		{tp, "text", "Enter some text", map[string]any{}},
		{tp, "text", "Enter some text", map[string]any{"rating": 3}},
		{rp, "rating", "Pick 1–5", map[string]any{}},
		{rp, "rating", "Pick 1–5", map[string]any{"text": "x"}},
	} {
		r := e.a.Do(t, "PUT", dailyBase+"/2026-10-26/notes/"+c.id, c.body, e.cookie)
		if r.Status != 400 || r.Field(c.field) != c.msg {
			t.Errorf("%s %v: %d %s", c.field, c.body, r.Status, r.Raw)
		}
	}
	r := e.a.Do(t, "GET", dailyBase+"?date=2026-10-26", nil, e.cookie)
	ps := r.Body["prompts"].([]any)
	if ps[0].(map[string]any)["text"] != "Kept" || ps[1].(map[string]any)["rating"] != 4.0 {
		t.Errorf("answers not kept: %v", ps)
	}
}

func TestDaily_DateErrors(t *testing.T) {
	e := setup(t)
	hid := e.habit(t, validHabit(nil), 201)["id"].(string)
	pid := e.prompt(t, "Mood", "rating", 201)["id"].(string)
	for _, r := range []struct {
		method, path string
		body         any
	}{
		{"GET", dailyBase + "?date=2026-10-28", nil},
		{"GET", dailyBase + "?date=27-10-2026", nil},
		{"PUT", dailyBase + "/2026-10-28/habits/" + hid, map[string]any{"value": 1}},
		{"PUT", dailyBase + "/2026-10-28/notes/" + pid, map[string]any{"rating": 3}},
	} {
		got := e.a.Do(t, r.method, r.path, r.body, e.cookie)
		if got.Status != 400 || got.Field("date") != "Pick today or an earlier day" {
			t.Errorf("%s %s: %d %s", r.method, r.path, got.Status, got.Raw)
		}
	}
}
