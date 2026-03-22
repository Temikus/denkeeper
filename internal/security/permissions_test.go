package security

import "testing"

func TestNewPermissionEngine_Supervised(t *testing.T) {
	pe, err := NewPermissionEngine("supervised")
	if err != nil {
		t.Fatalf("NewPermissionEngine(supervised): %v", err)
	}

	allowed := []string{"chat", "read_memory", "write_memory", "read_user_file", "write_user_file", "use_tools", "use_read_only_tools"}
	for _, action := range allowed {
		if !pe.CanExecute(action) {
			t.Errorf("supervised: CanExecute(%q) = false, want true", action)
		}
	}

	denied := []string{"create_skill", "modify_schedule", "change_fallback", "execute_shell", "access_filesystem", ""}
	for _, action := range denied {
		if pe.CanExecute(action) {
			t.Errorf("supervised: CanExecute(%q) = true, want false", action)
		}
	}
}

func TestNewPermissionEngine_Autonomous(t *testing.T) {
	pe, err := NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("NewPermissionEngine(autonomous): %v", err)
	}

	allowed := []string{
		"chat", "read_memory", "write_memory",
		"read_user_file", "write_user_file",
		"use_tools", "use_read_only_tools",
		"create_skill", "modify_schedule", "change_fallback",
		"execute_shell", "access_filesystem",
	}
	for _, action := range allowed {
		if !pe.CanExecute(action) {
			t.Errorf("autonomous: CanExecute(%q) = false, want true", action)
		}
	}
}

func TestNewPermissionEngine_Restricted(t *testing.T) {
	pe, err := NewPermissionEngine("restricted")
	if err != nil {
		t.Fatalf("NewPermissionEngine(restricted): %v", err)
	}

	allowed := []string{"chat", "read_memory", "write_memory", "use_read_only_tools"}
	for _, action := range allowed {
		if !pe.CanExecute(action) {
			t.Errorf("restricted: CanExecute(%q) = false, want true", action)
		}
	}

	denied := []string{"read_user_file", "write_user_file", "use_tools", "create_skill", "modify_schedule", "execute_shell", "access_filesystem"}
	for _, action := range denied {
		if pe.CanExecute(action) {
			t.Errorf("restricted: CanExecute(%q) = true, want false", action)
		}
	}
}

func TestNewPermissionEngine_InvalidTier(t *testing.T) {
	_, err := NewPermissionEngine("superuser")
	if err == nil {
		t.Fatal("expected error for invalid tier, got nil")
	}
}

func TestNewPermissionEngine_EmptyTier(t *testing.T) {
	_, err := NewPermissionEngine("")
	if err == nil {
		t.Fatal("expected error for empty tier, got nil")
	}
}

func TestPermissionEngine_Tier(t *testing.T) {
	pe, err := NewPermissionEngine("autonomous")
	if err != nil {
		t.Fatalf("NewPermissionEngine: %v", err)
	}
	if pe.Tier() != "autonomous" {
		t.Errorf("Tier() = %q, want %q", pe.Tier(), "autonomous")
	}
}

func TestNewDenyAll(t *testing.T) {
	pe := NewDenyAll()

	actions := []string{"chat", "read_memory", "write_memory", "use_tools", "create_skill", ""}
	for _, action := range actions {
		if pe.CanExecute(action) {
			t.Errorf("NewDenyAll: CanExecute(%q) = true, want false", action)
		}
	}
}

func TestValidTier(t *testing.T) {
	for _, tier := range []string{"supervised", "autonomous", "restricted"} {
		if !ValidTier(tier) {
			t.Errorf("ValidTier(%q) = false, want true", tier)
		}
	}
	for _, tier := range []string{"", "admin", "superuser"} {
		if ValidTier(tier) {
			t.Errorf("ValidTier(%q) = true, want false", tier)
		}
	}
}
