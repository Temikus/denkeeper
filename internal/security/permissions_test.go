package security

import "testing"

func TestPermissionEngine_CanExecute(t *testing.T) {
	pe := NewPermissionEngine()

	allowed := []string{"chat", "read_memory", "write_memory"}
	for _, action := range allowed {
		if !pe.CanExecute(action) {
			t.Errorf("CanExecute(%q) = false, want true", action)
		}
	}

	denied := []string{"execute_code", "send_email", "delete_file", "sudo", ""}
	for _, action := range denied {
		if pe.CanExecute(action) {
			t.Errorf("CanExecute(%q) = true, want false", action)
		}
	}
}

func TestPermissionEngine_Tier(t *testing.T) {
	pe := NewPermissionEngine()
	if pe.Tier() != "supervised" {
		t.Errorf("Tier() = %q, want supervised", pe.Tier())
	}
}
