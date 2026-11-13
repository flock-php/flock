package repository

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		value     string
		wantOwner string
		wantName  string
		wantError bool
	}{
		{value: "flock-php/flock", wantOwner: "flock-php", wantName: "flock"},
		{value: "", wantError: true},
		{value: "flock", wantError: true},
		{value: "/flock", wantError: true},
		{value: "flock/", wantError: true},
		{value: "flock-php/flock/tools", wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			owner, name, err := Parse(tt.value)
			if (err != nil) != tt.wantError {
				t.Fatalf("Parse(%q) error = %v, wantError %t", tt.value, err, tt.wantError)
			}
			if owner != tt.wantOwner || name != tt.wantName {
				t.Fatalf("Parse(%q) = %q, %q; want %q, %q", tt.value, owner, name, tt.wantOwner, tt.wantName)
			}
		})
	}
}
