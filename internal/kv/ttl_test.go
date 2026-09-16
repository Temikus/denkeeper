package kv

import (
	"strings"
	"testing"
	"time"
)

func TestParseTTL_Days(t *testing.T) {
	d, err := ParseTTL("30d")
	if err != nil {
		t.Fatalf("ParseTTL: %v", err)
	}
	if want := 30 * 24 * time.Hour; d != want {
		t.Errorf("ParseTTL(\"30d\") = %v, want %v", d, want)
	}
}

func TestParseTTL_Weeks(t *testing.T) {
	d, err := ParseTTL("2w")
	if err != nil {
		t.Fatalf("ParseTTL: %v", err)
	}
	if want := 2 * 7 * 24 * time.Hour; d != want {
		t.Errorf("ParseTTL(\"2w\") = %v, want %v", d, want)
	}
}

func TestParseTTL_FractionalDay(t *testing.T) {
	d, err := ParseTTL("0.5d")
	if err != nil {
		t.Fatalf("ParseTTL: %v", err)
	}
	if want := 12 * time.Hour; d != want {
		t.Errorf("ParseTTL(\"0.5d\") = %v, want %v", d, want)
	}
}

func TestParseTTL_MixedDayHour(t *testing.T) {
	d, err := ParseTTL("1d12h")
	if err != nil {
		t.Fatalf("ParseTTL: %v", err)
	}
	if want := 36 * time.Hour; d != want {
		t.Errorf("ParseTTL(\"1d12h\") = %v, want %v", d, want)
	}
}

func TestParseTTL_MixedWeekDay(t *testing.T) {
	d, err := ParseTTL("1w3d")
	if err != nil {
		t.Fatalf("ParseTTL: %v", err)
	}
	if want := (7 + 3) * 24 * time.Hour; d != want {
		t.Errorf("ParseTTL(\"1w3d\") = %v, want %v", d, want)
	}
}

func TestParseTTL_PlainGoDurationPassthrough(t *testing.T) {
	d, err := ParseTTL("90m")
	if err != nil {
		t.Fatalf("ParseTTL: %v", err)
	}
	if want := 90 * time.Minute; d != want {
		t.Errorf("ParseTTL(\"90m\") = %v, want %v", d, want)
	}
}

func TestParseTTL_MultiTermGoDurationPassthrough(t *testing.T) {
	d, err := ParseTTL("1h30m")
	if err != nil {
		t.Fatalf("ParseTTL: %v", err)
	}
	if want := 90 * time.Minute; d != want {
		t.Errorf("ParseTTL(\"1h30m\") = %v, want %v", d, want)
	}
}

func TestParseTTL_InvalidInput(t *testing.T) {
	_, err := ParseTTL("30 days")
	if err == nil {
		t.Fatal("expected an error for \"30 days\"")
	}
	if !strings.Contains(err.Error(), "units") {
		t.Errorf("error should name the accepted units, got: %v", err)
	}
}

func TestParseTTL_UnknownUnitStillErrors(t *testing.T) {
	_, err := ParseTTL("5M")
	if err == nil {
		t.Fatal("expected an error for \"5M\" (case-sensitive, not a valid unit)")
	}
}

func TestParseTTL_EmptyStringErrors(t *testing.T) {
	// Callers special-case "" to mean "no expiry" before calling ParseTTL;
	// the parser itself has no expiry-string convention of its own, matching
	// time.ParseDuration("").
	_, err := ParseTTL("")
	if err == nil {
		t.Fatal("expected an error for empty string")
	}
}

func TestParseTTL_Negative(t *testing.T) {
	d, err := ParseTTL("-5m")
	if err != nil {
		t.Fatalf("ParseTTL(\"-5m\"): %v", err)
	}
	if want := -5 * time.Minute; d != want {
		t.Errorf("ParseTTL(\"-5m\") = %v, want %v", d, want)
	}
}

func TestParseTTL_NegativeDay(t *testing.T) {
	d, err := ParseTTL("-1d")
	if err != nil {
		t.Fatalf("ParseTTL(\"-1d\"): %v", err)
	}
	if want := -24 * time.Hour; d != want {
		t.Errorf("ParseTTL(\"-1d\") = %v, want %v", d, want)
	}
}

func TestParseTTL_Zero(t *testing.T) {
	d, err := ParseTTL("0s")
	if err != nil {
		t.Fatalf("ParseTTL(\"0s\"): %v", err)
	}
	if d != 0 {
		t.Errorf("ParseTTL(\"0s\") = %v, want 0", d)
	}
}

func TestParseTTL_Overflow(t *testing.T) {
	_, err := ParseTTL("100000000000w")
	if err == nil {
		t.Fatal("expected an overflow error")
	}
}
