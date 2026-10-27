package daily_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"beprod/internal/models"
)

const promptsBase = "/api/v1/note-prompts"

func (e env) prompt(t *testing.T, label, kind string, want int) map[string]any {
	t.Helper()
	r := e.a.Do(t, "POST", promptsBase, map[string]any{"label": label, "kind": kind}, e.cookie)
	if r.Status != want {
		t.Fatalf("create prompt: %d, want %d: %s", r.Status, want, r.Raw)
	}
	p, _ := r.Body["prompt"].(map[string]any)
	return p
}

func (e env) labels(t *testing.T, key string) []string {
	t.Helper()
	r := e.a.Do(t, "GET", promptsBase, nil, e.cookie)
	list, ok := r.Body[key].([]any)
	if r.Status != 200 || !ok {
		t.Fatalf("list: %d %s", r.Status, r.Raw)
	}
	out := []string{}
	for _, p := range list {
		out = append(out, p.(map[string]any)["label"].(string))
	}
	return out
}

func TestPrompts_CreateList(t *testing.T) {
	e := setup(t)
	p := e.prompt(t, " Mood ", "rating", 201)
	want := map[string]any{"label": "Mood", "kind": "rating", "hidden": false}
	for k, v := range want {
		if !reflect.DeepEqual(p[k], v) {
			t.Errorf("%s = %v, want %v", k, p[k], v)
		}
	}
	e.prompt(t, "Grateful for…", "text", 201)
	if got := e.labels(t, "prompts"); !reflect.DeepEqual(got, []string{"Mood", "Grateful for…"}) {
		t.Errorf("order = %v", got)
	}
	if got := e.labels(t, "hidden"); len(got) != 0 {
		t.Errorf("hidden = %v", got)
	}
}

func TestPrompts_Validation(t *testing.T) {
	e := setup(t)
	e.prompt(t, "Mood", "rating", 201)
	cases := []struct{ name, label, kind, field, msg string }{
		{"empty label", "", "text", "label", "Label must be 1–60 characters"},
		{"long label", strings.Repeat("a", 61), "text", "label", "Label must be 1–60 characters"},
		{"bad kind", "X", "emoji", "kind", "Choose text or rating"},
		{"duplicate", "mood", "text", "label", "You already have this prompt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := e.a.Do(t, "POST", promptsBase, map[string]any{"label": c.label, "kind": c.kind}, e.cookie)
			if r.Status != 400 || r.Field(c.field) != c.msg {
				t.Errorf("got %d %s, want 400 %s=%q", r.Status, r.Raw, c.field, c.msg)
			}
		})
	}
}

func TestPrompts_Limit20(t *testing.T) {
	const msg = "You can have up to 20 prompts"
	e := setup(t)
	var first string
	for i := 0; i < 20; i++ {
		p := e.prompt(t, fmt.Sprintf("Prompt %d", i), "text", 201)
		if i == 0 {
			first = p["id"].(string)
		}
	}
	r := e.a.Do(t, "POST", promptsBase, map[string]any{"label": "One more", "kind": "text"}, e.cookie)
	if r.Status != 400 || r.Field("label") != msg {
		t.Fatalf("21st: %d %s", r.Status, r.Raw)
	}
	if r := e.a.Do(t, "PATCH", promptsBase+"/"+first, map[string]any{"hidden": true}, e.cookie); r.Status != 200 {
		t.Fatalf("hide: %d %s", r.Status, r.Raw)
	}
	e.prompt(t, "Replacement", "text", 201)
	r = e.a.Do(t, "PATCH", promptsBase+"/"+first, map[string]any{"hidden": false}, e.cookie)
	if r.Status != 400 || r.Field("label") != msg {
		t.Fatalf("unhide: %d %s", r.Status, r.Raw)
	}
}

func TestPrompts_HideUnhide(t *testing.T) {
	e := setup(t)
	id := e.prompt(t, "Mood", "rating", 201)["id"].(string)
	r := e.a.Do(t, "PATCH", promptsBase+"/"+id, map[string]any{"hidden": true, "kind": "text"}, e.cookie)
	p, _ := r.Body["prompt"].(map[string]any)
	if r.Status != 200 || p["hidden"] != true || p["kind"] != "rating" {
		t.Fatalf("hide: %d %s", r.Status, r.Raw)
	}
	if got := e.labels(t, "hidden"); !reflect.DeepEqual(got, []string{"Mood"}) {
		t.Errorf("hidden = %v", got)
	}
	e.prompt(t, "Mood", "rating", 201) // label is free while hidden
	r = e.a.Do(t, "PATCH", promptsBase+"/"+id, map[string]any{"hidden": false}, e.cookie)
	if r.Status != 400 || r.Field("label") != "You already have this prompt" {
		t.Fatalf("unhide clash: %d %s", r.Status, r.Raw)
	}
}

func TestPrompts_UnhideGoesLast(t *testing.T) {
	e := setup(t)
	ids := map[string]string{}
	for _, n := range []string{"A", "B", "C"} {
		ids[n] = e.prompt(t, n, "text", 201)["id"].(string)
	}
	e.a.Do(t, "PATCH", promptsBase+"/"+ids["A"], map[string]any{"hidden": true}, e.cookie)
	if r := e.a.Do(t, "PATCH", promptsBase+"/"+ids["A"], map[string]any{"hidden": false}, e.cookie); r.Status != 200 {
		t.Fatalf("unhide: %d %s", r.Status, r.Raw)
	}
	if got := e.labels(t, "prompts"); !reflect.DeepEqual(got, []string{"B", "C", "A"}) {
		t.Errorf("order = %v", got)
	}
}

func TestPrompts_Order(t *testing.T) {
	e := setup(t)
	ids := map[string]string{}
	for _, n := range []string{"A", "B", "C"} {
		ids[n] = e.prompt(t, n, "text", 201)["id"].(string)
	}
	if s := e.a.Do(t, "PUT", promptsBase+"/order", map[string]any{"ids": []string{ids["C"], ids["A"], ids["B"]}}, e.cookie).Status; s != 204 {
		t.Fatalf("order: %d", s)
	}
	if got := e.labels(t, "prompts"); !reflect.DeepEqual(got, []string{"C", "A", "B"}) {
		t.Errorf("order = %v", got)
	}
	for _, body := range [][]string{{ids["C"], ids["A"]}, {ids["C"], ids["A"], ids["A"]}} {
		r := e.a.Do(t, "PUT", promptsBase+"/order", map[string]any{"ids": body}, e.cookie)
		if r.Status != 400 || r.ErrCode() != "INVALID_ORDER" {
			t.Errorf("bad order %v: %d %s", body, r.Status, r.Raw)
		}
	}
}

func TestPrompts_DeleteRemovesEntries(t *testing.T) {
	t.Skip("needs Task 4 PUT /daily route")
	e := setup(t)
	id := e.prompt(t, "Mood", "rating", 201)["id"].(string)
	if r := e.a.Do(t, "PUT", "/api/v1/daily/2026-10-27/notes/"+id, map[string]any{"rating": 4}, e.cookie); r.Status != 200 {
		t.Fatalf("answer: %d %s", r.Status, r.Raw)
	}
	if r := e.a.Do(t, "DELETE", promptsBase+"/"+id, nil, e.cookie); r.Status != 204 {
		t.Fatalf("delete: %d %s", r.Status, r.Raw)
	}
	var n int64
	e.a.DB.Model(&models.NoteEntry{}).Where("prompt_id = ?", id).Count(&n)
	if n != 0 {
		t.Errorf("entries left: %d", n)
	}
	if r := e.a.Do(t, "PATCH", promptsBase+"/"+id, map[string]any{"label": "X"}, e.cookie); r.Status != 404 {
		t.Errorf("patch deleted: %d", r.Status)
	}
}
