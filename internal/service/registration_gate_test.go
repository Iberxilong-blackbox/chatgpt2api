package service

import (
	"testing"
	"time"
)

func TestRegistrationGateConsumesIdentityOnce(t *testing.T) {
	gate := NewRegistrationGateService(newTestStorageBackend(t))
	item, err := gate.Add(" friend-id ", "Friend")
	if err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if item["identity_id"] != "friend-id" || item["label"] != "Friend" {
		t.Fatalf("Add() item = %#v", item)
	}
	if err := gate.ValidateAvailable("friend-id"); err != nil {
		t.Fatalf("ValidateAvailable() error = %v", err)
	}
	consumed, err := gate.Consume("friend-id", "user_1", "alice")
	if err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	if consumed["used"] != true || consumed["used_by_user_id"] != "user_1" || consumed["used_by_username"] != "alice" {
		t.Fatalf("consumed item = %#v", consumed)
	}
	if err := gate.ValidateAvailable("friend-id"); err == nil {
		t.Fatal("used identity ID still validates")
	}
	if _, err := gate.Consume("friend-id", "user_2", "bob"); err == nil {
		t.Fatal("second Consume() succeeded")
	}
}

func TestRegistrationGateKeepsUsedIdentityAfterUserDeleted(t *testing.T) {
	gate := NewRegistrationGateService(newTestStorageBackend(t))
	if _, err := gate.Add("friend-id", "Friend"); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if _, err := gate.Consume("friend-id", "user_1", "alice"); err != nil {
		t.Fatalf("Consume() error = %v", err)
	}
	if err := gate.MarkUserDeleted("user_1"); err != nil {
		t.Fatalf("MarkUserDeleted() error = %v", err)
	}
	items := gate.List()
	if len(items) != 1 || items[0]["used"] != true || items[0]["used_user_deleted"] != true {
		t.Fatalf("deleted user mapping = %#v", items)
	}
	if deleted, err := gate.Delete(items[0]["id"].(string)); err == nil || deleted {
		t.Fatalf("Delete(used) = %v, %v; want blocked", deleted, err)
	}
}

func TestRegistrationGateUsedCountOnLocalDate(t *testing.T) {
	gate := NewRegistrationGateService(newTestStorageBackend(t))
	if _, err := gate.Add("today-id", "Today"); err != nil {
		t.Fatalf("Add(today) error = %v", err)
	}
	if _, err := gate.Consume("today-id", "user_1", "alice"); err != nil {
		t.Fatalf("Consume(today) error = %v", err)
	}
	if got := gate.UsedCountOnLocalDate(time.Now()); got != 1 {
		t.Fatalf("UsedCountOnLocalDate(today) = %d, want 1", got)
	}
}
