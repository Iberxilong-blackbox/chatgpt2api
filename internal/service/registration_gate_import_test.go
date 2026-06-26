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
