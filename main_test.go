package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOrderValidate(t *testing.T) {
	parse := func(s string) *orderReq {
		var r orderReq
		if err := json.Unmarshal([]byte(s), &r); err != nil {
			t.Fatal(err)
		}
		return &r
	}
	tooMany := `{"items":[` + strings.Repeat(`{"menu_id":1,"qty":1},`, 50) + `{"menu_id":1,"qty":1}]}`
	for _, s := range []string{
		`{"items":[]}`,
		`{"items":[{"menu_id":1,"qty":0}]}`,
		`{"items":[{"menu_id":1,"qty":100}]}`,
		tooMany,
	} {
		if err := parse(s).validate(); err == nil {
			t.Errorf("expected error for %.60s", s)
		}
	}
	if err := parse(`{"items":[{"menu_id":1,"qty":2,"options":["เผ็ดมาก"]},{"menu_id":1,"qty":1}]}`).validate(); err != nil {
		t.Fatal(err)
	}
}

func TestPickOptions(t *testing.T) {
	groups := []optionGroup{
		{"ความเผ็ด", []string{"ไม่เผ็ด", "เผ็ดน้อย", "เผ็ดมาก"}},
		{"ความหวาน", []string{"ไม่หวาน", "หวานน้อย", "หวานมาก"}},
	}
	for _, chosen := range [][]string{
		{"เผ็ดน้อย", "เผ็ดมาก"}, // two from one group
		{"เผ็ดมาก", "เผ็ดมาก"},  // duplicate
		{"ใส่ไข่"},              // not on this dish
	} {
		if _, err := pickOptions(groups, chosen); err == nil {
			t.Errorf("expected error for %v", chosen)
		}
	}
	if _, err := pickOptions(groups[:1], []string{"หวานน้อย"}); err == nil {
		t.Error("sweetness must be rejected on a dish with only spiciness")
	}

	got, err := pickOptions(groups, []string{"หวานน้อย", "เผ็ดมาก"})
	if err != nil || strings.Join(got, ",") != "เผ็ดมาก,หวานน้อย" {
		t.Fatalf("want group order, got %v %v", got, err)
	}
	got, err = pickOptions(groups, nil)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("no options must be an empty non-nil slice, got %#v %v", got, err)
	}
}

func TestCredentialsValidate(t *testing.T) {
	bad := []credentials{
		{"ab", "secret1"},
		{"   ", "secret1"},
		{strings.Repeat("ก", 31), "secret1"},
		{"somchai", "12345"},
		{"somchai", strings.Repeat("x", 73)},
	}
	for _, cr := range bad {
		if err := cr.validate(); err == nil {
			t.Errorf("expected error for %+v", cr)
		}
	}

	cr := credentials{" สมชาย ", "secret1"}
	if err := cr.validate(); err != nil || cr.Username != "สมชาย" {
		t.Fatalf("got %q %v", cr.Username, err)
	}
}
