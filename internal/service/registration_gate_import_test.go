package service

import "testing"

func TestMergeRegistrationIdentityIDsPreservesExistingAndSkipsDuplicates(t *testing.T) {
	existing := map[string]any{"items": []map[string]any{{
		"id":          "rid_existing",
		"identity_id": "friend-1",
		"label":       "Existing",
		"enabled":     true,
		"used":        true,
	}}}

	doc, stats, err := MergeRegistrationIdentityIDs(existing, []string{"friend-1", " friend-2 ", "friend-2"}, "Batch")
	if err != nil {
		t.Fatalf("MergeRegistrationIdentityIDs() error = %v", err)
	}
	items := doc["items"].([]map[string]any)
	if len(items) != 2 {
		t.Fatalf("items len = %d, want 2", len(items))
	}
	if stats.Existing != 1 || stats.Input != 3 || stats.Added != 1 || stats.Duplicates != 2 {
		t.Fatalf("stats = %#v", stats)
	}
	if items[0]["identity_id"] != "friend-1" || items[0]["used"] != true {
		t.Fatalf("existing item was not preserved: %#v", items[0])
	}
	if items[1]["identity_id"] != "friend-2" || items[1]["label"] != "Batch" || items[1]["enabled"] != true || items[1]["used"] != false {
		t.Fatalf("new item = %#v", items[1])
	}
}

func TestMergeRegistrationIdentityIDsRejectsInvalidID(t *testing.T) {
	_, stats, err := MergeRegistrationIdentityIDs(nil, []string{"ok", "bad\nid"}, "")
	if err == nil {
		t.Fatal("MergeRegistrationIdentityIDs() returned nil error")
	}
	if stats.InvalidRows != 1 {
		t.Fatalf("InvalidRows = %d, want 1", stats.InvalidRows)
	}
}

func TestRegistrationIdentityIDsFromDocumentSupportsCommonShapes(t *testing.T) {
	cases := []struct {
		name string
		raw  any
		want []string
	}{
		{
			name: "document items",
			raw:  map[string]any{"items": []any{map[string]any{"identity_id": "friend-1"}, map[string]any{"identity_id": "friend-2"}}},
			want: []string{"friend-1", "friend-2"},
		},
		{
			name: "identity_ids",
			raw:  map[string]any{"identity_ids": []any{"friend-3", " friend-4 "}},
			want: []string{"friend-3", "friend-4"},
		},
		{
			name: "string array",
			raw:  []any{"friend-5"},
			want: []string{"friend-5"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RegistrationIdentityIDsFromDocument(tc.raw)
			if len(got) != len(tc.want) {
				t.Fatalf("len = %d, want %d: %#v", len(got), len(tc.want), got)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Fatalf("got = %#v, want %#v", got, tc.want)
				}
			}
		})
	}
}

func TestRegistrationGateImportSkipsExistingUsedIdentity(t *testing.T) {
	gate := NewRegistrationGateService(newTestStorageBackend(t))
	if _, err := gate.Add("friend-1", "Original"); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if _, err := gate.Consume("friend-1", "user_1", "alice"); err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	stats, err := gate.ImportIdentityIDs([]string{"friend-1", "friend-2"}, "Batch")
	if err != nil {
		t.Fatalf("ImportIdentityIDs() error = %v", err)
	}
	if stats.Existing != 1 || stats.Input != 2 || stats.Added != 1 || stats.Duplicates != 1 {
		t.Fatalf("stats = %#v", stats)
	}
	items := gate.List()
	if len(items) != 2 {
		t.Fatalf("items len = %d, want 2", len(items))
	}
	if items[0]["identity_id"] != "friend-1" || items[0]["used"] != true || items[0]["label"] != "Original" {
		t.Fatalf("existing item was overwritten: %#v", items[0])
	}
	if items[1]["identity_id"] != "friend-2" || items[1]["label"] != "Batch" || items[1]["used"] != false {
		t.Fatalf("new item = %#v", items[1])
	}
}
